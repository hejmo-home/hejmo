// Command hejmo runs the Hejmo box.
//
// Usage:
//
//	hejmo              start the box
//	hejmo healthcheck  exit 0 if the local box answers /healthz (for Docker)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // DST rules independent of the host system

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
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
}

func loadConfig(getenv func(string) string) config {
	cfg := config{Addr: ":8080", HTTPSAddr: ":8443", PublicHTTPSPort: "443", DataDir: "/data"}
	if v := getenv("HEJMO_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("HEJMO_HTTPS_ADDR"); v != "" {
		cfg.HTTPSAddr = v
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
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

	handler := server.New(server.Options{DB: db, PKI: certs, Public: public, HTTPSPort: cfg.PublicHTTPSPort, Logger: logger, Sync: syncer, Done: ctx.Done()})

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
