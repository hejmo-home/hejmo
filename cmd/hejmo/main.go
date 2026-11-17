// Command hejmo runs the Hejmo box.
//
// Usage:
//
//	hejmo              start the box
//	hejmo healthcheck  exit 0 if the local box answers /healthz (for Docker)
//	hejmo reset-otp <username>
//	                   reset an account's one-time codes (run inside the
//	                   container; it opens the database directly and is not
//	                   reachable over the network)
//	hejmo backup FILE  write an online backup of the database to FILE (for the update script)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // DST rules independent of the host system

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/weather"
)

type config struct {
	Addr            string // HTTP listen address (redirects to HTTPS), HEJMO_ADDR
	HTTPSAddr       string // HTTPS listen address, HEJMO_HTTPS_ADDR
	PublicHTTPSPort string // port clients reach HTTPS on, HEJMO_PUBLIC_HTTPS_PORT
	DataDir         string // persistent data, HEJMO_DATA_DIR
	// SecretKeyFile holds the key that seals stored credentials,
	// HEJMO_SECRET_KEY_FILE (default: secret.key in the data volume). Point it
	// at a Docker secret to keep the key off the data volume.
	SecretKeyFile string
	// UpdateDir is the data directory shared with the install script's host
	// updater (ADR 0018); empty on a plain Docker Compose install, which has
	// none. HEJMO_HOST_UPDATER=1 is set by the install script's compose file.
	UpdateDir string
}

func loadConfig(getenv func(string) string) config {
	cfg := config{Addr: ":8080", HTTPSAddr: ":8443", PublicHTTPSPort: "443", DataDir: "/data"}
	if v := getenv("HEJMO_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("HEJMO_HTTPS_ADDR"); v != "" {
		cfg.HTTPSAddr = v
	}
	// Clients reach HTTPS on the port the box listens on unless a mapping
	// (Docker, router) says otherwise: then the variable names the public port.
	if _, port, err := net.SplitHostPort(cfg.HTTPSAddr); err == nil && port != "" {
		cfg.PublicHTTPSPort = port
	}
	if v := getenv("HEJMO_PUBLIC_HTTPS_PORT"); v != "" {
		cfg.PublicHTTPSPort = v
	}
	if v := getenv("HEJMO_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	cfg.SecretKeyFile = filepath.Join(cfg.DataDir, "secret.key")
	if v := getenv("HEJMO_SECRET_KEY_FILE"); v != "" {
		cfg.SecretKeyFile = v
	}
	if getenv("HEJMO_HOST_UPDATER") == "1" {
		cfg.UpdateDir = cfg.DataDir
	}
	return cfg
}

func main() {
	cfg := loadConfig(os.Getenv)
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(localURL(cfg.Addr)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		dest := ""
		if len(os.Args) > 2 {
			dest = os.Args[2]
		}
		if err := backup(cfg, dest); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if len(os.Args) > 1 && os.Args[1] == "reset-otp" {
		logger = commandLogger(os.Stderr, "/proc/1/fd/1", os.Getpid())
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: hejmo reset-otp <username>")
			os.Exit(2)
		}
		if err := resetOTP(cfg, os.Args[2], logger); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("One-time codes of %s are reset. At the next sign-in, %s sets up new ones.\n", os.Args[2], os.Args[2])
		return
	}
	if err := run(cfg, logger); err != nil {
		logger.Error("box stopped", "err", err)
		os.Exit(1)
	}
}

func run(cfg config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, sealer, err := openDatabase(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	certs, err := pki.Open(filepath.Join(cfg.DataDir, "tls"), pki.WithLogger(logger))
	if err != nil {
		return err
	}

	// A public certificate (ADR 0014), if the family chose a name in the
	// wizard: its DNS token is sealed with a key file (ADR 0012), by default
	// next to the database.
	public, err := publiccert.New(filepath.Join(cfg.DataDir, "tls", "public"), db, sealer, publiccert.WithLogger(logger))
	if err != nil {
		return err
	}
	defer public.Close()
	// A setup that cannot be resumed (for example a lost key file) must not
	// take the web interface down: parents are warned there, and the box
	// serves HTTPS with its own CA meanwhile (ensureHTTPS).
	if err := public.Start(ctx); err != nil {
		logger.Error("public HTTPS not started; falling back to the box's own CA", "err", err)
	}
	ensureHTTPS(db, certs, time.Now(), logger)

	// The scheduler checks for due calendars every minute; each calendar
	// syncs on its own interval (60 minutes for ICS by default).
	syncer := calsync.New(db, nil, nil)
	syncer.SetLogger(logger)
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		syncer.Run(ctx, time.Minute)
	}()
	defer func() { stop(); <-schedulerDone }()
	// Background pushes of box changes finish before the database closes.
	defer syncer.Wait()

	// The kiosk runner switches the front light of displays with a kiosk app
	// off at sunset and in quiet mode (ADR 0005, 0015), checked every minute.
	kioskRunner := server.NewKioskRunner(server.KioskOptions{DB: db, Logger: logger})
	kioskDone := make(chan struct{})
	go func() {
		defer close(kioskDone)
		kioskRunner.Run(ctx, time.Minute)
	}()
	defer func() { stop(); <-kioskDone }()

	// The weather chip's reading: the box asks Open-Meteo every 15 minutes, a
	// display never does (ADR 0016).
	forecast := weather.New()
	weatherDone := make(chan struct{})
	go func() {
		defer close(weatherDone)
		forecast.Run(ctx, 15*time.Minute, homeOf{db}, func(err error) { logger.Warn("weather not refreshed", "err", err) })
	}()
	defer func() { stop(); <-weatherDone }()

	handler := server.New(server.Options{DB: db, PKI: certs, Public: public, HTTPSPort: cfg.PublicHTTPSPort, Logger: logger, Sync: syncer, Weather: forecast, Done: ctx.Done(), UpdateDir: cfg.UpdateDir, PhotoDir: cfg.DataDir})

	// HTTP serves the setup wizard and redirects to HTTPS once the box has its
	// certificate; HTTPS serves everything else (ADR 0014).
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	httpsSrv := &http.Server{
		Addr:              cfg.HTTPSAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsConfig(public, certs),
	}
	servers := []*http.Server{httpSrv, httpsSrv}
	// Whatever way run returns, the servers stop before syncer.Wait runs (defers
	// run last-in first), so no handler can start a push while Wait is waiting.
	defer func() {
		stopped, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, srv := range servers {
			_ = srv.Shutdown(stopped)
		}
	}()
	errc := make(chan error, len(servers))
	go func() {
		logger.Info("box listening (HTTP)", "addr", cfg.Addr)
		errc <- httpSrv.ListenAndServe()
	}()
	go func() {
		logger.Info("box listening (HTTPS)", "addr", cfg.HTTPSAddr)
		errc <- httpsSrv.ListenAndServeTLS("", "")
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdown); err != nil {
			return err
		}
	}
	for range servers {
		if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

// commandLogger logs a command run inside the box's container. `docker compose
// logs` shows only the output of the main process (PID 1), so the line is also
// written to that process's stdout when this is not PID 1 and it is reachable.
func commandLogger(stderr io.Writer, pid1Stdout string, pid int) *slog.Logger {
	var w io.Writer = stderr
	if pid != 1 {
		if f, err := os.OpenFile(pid1Stdout, os.O_WRONLY|os.O_APPEND, 0); err == nil {
			w = io.MultiWriter(stderr, f)
		}
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// resetOTP is the last way back (ADR 0017): it removes an account's one-time
// code secret and recovery codes and ends its browser sessions. Access to the
// box's shell counts as proof of ownership.
func resetOTP(cfg config, username string, logger *slog.Logger) error {
	db, _, err := openDatabase(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	acc, err := db.AccountByUsername(strings.TrimSpace(username))
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no account named %q", username)
	}
	if err != nil {
		return err
	}
	if err := db.ResetOTP(acc.PersonID); err != nil {
		return err
	}
	logger.Info("one-time codes reset", "account", strings.ToLower(acc.Username), "via", "command on the box")
	return nil
}

// openDatabase opens the box's database and the key file that seals stored
// credentials (calendar passwords, the DNS token; ADR 0012). The key file is
// generated on first start (mode 0600). A key file named by
// HEJMO_SECRET_KEY_FILE must already exist, so a missing Docker secret does
// not silently start the box with a new key. The database gets the sealer, and
// the caller passes it on to the public certificate manager.
func openDatabase(cfg config) (*store.DB, *secret.Sealer, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(cfg.SecretKeyFile); errors.Is(err, os.ErrNotExist) && os.Getenv("HEJMO_SECRET_KEY_FILE") != "" {
		return nil, nil, fmt.Errorf("HEJMO_SECRET_KEY_FILE %s does not exist", cfg.SecretKeyFile)
	}
	sealer, err := secret.Open(cfg.SecretKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("credential key: %w", err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "hejmo.db"))
	if err != nil {
		return nil, nil, err
	}
	db.SetSecrets(sealer)
	return db, sealer, nil
}

// ensureHTTPS makes sure the box can serve HTTPS whenever the family chose a
// public name: the box then also has its own CA for that name, which serves
// every handshake the public certificate cannot (ADR 0014, decision Q27). That
// covers a certificate that cannot be loaded and one that cannot be obtained
// (token readable, certificate missing, CA or deSEC unreachable), where the
// manager is ready but has nothing to serve. The public certificate stays
// preferred, see tlsConfig. Without a stored public setup it does nothing,
// and neither before setup is complete: the wizard's own-CA choice then
// provisions the CA for the address the browser used, and a CA made earlier
// for the public name would make that choice fail. Plain HTTP stays only for
// the wizard before HTTPS is configured.
func ensureHTTPS(db *store.DB, certs *pki.Store, now time.Time, logger *slog.Logger) {
	if certs.Ready() || !db.Configured() {
		return
	}
	saved, ok, err := db.PublicHTTPS()
	if err != nil || !ok {
		if err != nil {
			logger.Error("read stored public HTTPS setup", "err", err)
		}
		return
	}
	if err := certs.Provision(saved.Name, now); err != nil {
		logger.Error("own CA fallback failed; HTTPS may be unavailable", "err", err)
		return
	}
	logger.Info("own CA kept as a standby; phones need to trust it only if the public certificate is unavailable, see /ca", "name", saved.Name)
}

// tlsConfig serves the public certificate when there is one and the box's own
// CA certificate otherwise, also when the public certificate cannot be
// produced for a handshake.
func tlsConfig(public *publiccert.Manager, certs *pki.Store) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if public.Ready() {
			c, err := public.GetCertificate(h)
			if err == nil || !certs.Ready() {
				return c, err
			}
		}
		return certs.GetCertificate(h)
	}}
}

// backup writes a consistent copy of the database to dest with SQLite's online
// backup while the box keeps running (ADR 0018).
func backup(cfg config, dest string) error {
	if dest == "" {
		return errors.New("usage: hejmo backup FILE")
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "hejmo.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Backup(dest)
}

// healthcheck asks the box at baseURL for /healthz.
func healthcheck(baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: status %d", res.StatusCode)
	}
	return nil
}

// localURL turns a listen address into a URL reachable from the same host.
func localURL(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = "8080"
	}
	return "http://127.0.0.1:" + port
}

// homeOf gives the weather service the household's home location.
type homeOf struct{ db *store.DB }

func (h homeOf) Home() (lat, lon float64, err error) {
	hh, err := h.db.HomeHousehold()
	if errors.Is(err, store.ErrNotFound) {
		return 0, 0, weather.ErrNoHome
	}
	if err != nil {
		return 0, 0, err
	}
	return hh.Latitude, hh.Longitude, nil
}
