package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/weather"
)

func TestConfigDefaults(t *testing.T) {
	cfg := loadConfig(func(string) string { return "" })
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", cfg.DataDir)
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	env := map[string]string{"HEJMO_ADDR": "127.0.0.1:9000", "HEJMO_DATA_DIR": "/tmp/hejmo"}
	cfg := loadConfig(func(k string) string { return env[k] })
	if cfg.Addr != "127.0.0.1:9000" || cfg.DataDir != "/tmp/hejmo" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestSecretKeyFileDefaultsToTheDataVolumeAndCanBeMoved(t *testing.T) {
	cfg := loadConfig(func(k string) string {
		return map[string]string{"HEJMO_DATA_DIR": "/vol"}[k]
	})
	if cfg.SecretKeyFile != "/vol/secret.key" {
		t.Errorf("SecretKeyFile = %q, want /vol/secret.key", cfg.SecretKeyFile)
	}
	cfg = loadConfig(func(k string) string {
		return map[string]string{"HEJMO_DATA_DIR": "/vol", "HEJMO_SECRET_KEY_FILE": "/run/secrets/hejmo_key"}[k]
	})
	if cfg.SecretKeyFile != "/run/secrets/hejmo_key" {
		t.Errorf("SecretKeyFile = %q, want the Docker secret path", cfg.SecretKeyFile)
	}
}

func TestStartOnAFreshVolumeCreatesTheKeyFileAndRestartReusesIt(t *testing.T) {
	dir := t.TempDir()
	cfg := config{DataDir: dir, SecretKeyFile: filepath.Join(dir, "secret.key")}
	db, _, err := openDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.SecretKeyFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file after first start: %v, %v", info, err)
	}
	first, _ := os.ReadFile(cfg.SecretKeyFile)
	h, _ := db.CreateHousehold(store.Household{Name: "Home"})
	if _, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{URL: "https://x.example/", Username: "u", Password: "p", Now: time.Now()}); err != nil {
		t.Fatalf("the opened database cannot seal credentials: %v", err)
	}
	db.Close()

	db, _, err = openDatabase(cfg) // restart
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if again, _ := os.ReadFile(cfg.SecretKeyFile); string(again) != string(first) {
		t.Fatal("the key file changed on restart")
	}
	conns, _ := db.Connections(h.ID)
	if _, pass, err := db.ConnectionCredentials(h.ID, conns[0].ID); err != nil || pass != "p" {
		t.Fatalf("credentials after restart = %q, %v", pass, err)
	}
}

func TestHTTPSConfig(t *testing.T) {
	cfg := loadConfig(func(string) string { return "" })
	if cfg.HTTPSAddr != ":8443" || cfg.PublicHTTPSPort != "8443" {
		t.Errorf("defaults: HTTPSAddr=%q PublicHTTPSPort=%q", cfg.HTTPSAddr, cfg.PublicHTTPSPort)
	}
	env := map[string]string{"HEJMO_HTTPS_ADDR": ":9443", "HEJMO_PUBLIC_HTTPS_PORT": "9443"}
	cfg = loadConfig(func(k string) string { return env[k] })
	if cfg.HTTPSAddr != ":9443" || cfg.PublicHTTPSPort != "9443" {
		t.Errorf("from env: %+v", cfg)
	}
}

// Run natively, the box listens on :8443; clients then reach HTTPS there, so
// the public port follows HEJMO_HTTPS_ADDR unless it is set explicitly.
func TestPublicHTTPSPortDefaultsToThePortOfTheHTTPSAddress(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"native defaults", nil, "8443"},
		{"custom address", map[string]string{"HEJMO_HTTPS_ADDR": "0.0.0.0:9443"}, "9443"},
		{"explicit port wins", map[string]string{"HEJMO_HTTPS_ADDR": ":8443", "HEJMO_PUBLIC_HTTPS_PORT": "443"}, "443"},
		{"address without port", map[string]string{"HEJMO_HTTPS_ADDR": "garbage"}, "443"},
	}
	for _, c := range cases {
		cfg := loadConfig(func(k string) string { return c.env[k] })
		if cfg.PublicHTTPSPort != c.want {
			t.Errorf("%s: PublicHTTPSPort = %q, want %q", c.name, cfg.PublicHTTPSPort, c.want)
		}
	}
}

// Acceptance: ports 443 and 80 are configurable in Compose.
func TestComposeMakesBothPortsConfigurable(t *testing.T) {
	raw, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, want := range []string{
		`"${HEJMO_HTTP_PORT:-80}:8080"`,
		`"${HEJMO_HTTPS_PORT:-443}:8443"`,
		`HEJMO_PUBLIC_HTTPS_PORT: ${HEJMO_HTTPS_PORT:-443}`,
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml misses %s", want)
		}
	}
}

func TestHealthcheckCommand(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	defer ok.Close()
	if err := healthcheck(ok.URL); err != nil {
		t.Errorf("healthy box: %v", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	if err := healthcheck(down.URL); err == nil {
		t.Error("unhealthy box: want error")
	}
}

func TestLocalURL(t *testing.T) {
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080",
		"0.0.0.0:80":     "http://127.0.0.1:80",
		"127.0.0.1:9000": "http://127.0.0.1:9000",
		"[::]:8080":      "http://127.0.0.1:8080",
	}
	for addr, want := range cases {
		if got := localURL(addr); got != want {
			t.Errorf("localURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// ADR 0012: an environment variable can point the key to another file, such
// as a Docker secret.
func TestSecretKeyFileDefaultsToTheDataDirAndCanBeMoved(t *testing.T) {
	cfg := loadConfig(func(k string) string {
		if k == "HEJMO_DATA_DIR" {
			return "/srv/hejmo"
		}
		return ""
	})
	if cfg.SecretKeyFile != "/srv/hejmo/secret.key" {
		t.Errorf("default = %q, want secret.key in the data directory", cfg.SecretKeyFile)
	}
	env := map[string]string{"HEJMO_DATA_DIR": "/srv/hejmo", "HEJMO_SECRET_KEY_FILE": "/run/secrets/hejmo_key"}
	cfg = loadConfig(func(k string) string { return env[k] })
	if cfg.SecretKeyFile != "/run/secrets/hejmo_key" {
		t.Errorf("from env = %q", cfg.SecretKeyFile)
	}
}

// Decision Q27: if the public certificate cannot be loaded, the box serves
// HTTPS with its own CA and warns parents; it never drops to plain HTTP.
func TestFailedPublicCertificateFallsBackToTheOwnCAWithAWarning(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	certs, err := pki.Open(filepath.Join(dir, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secret.Open(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	public, err := publiccert.New(filepath.Join(dir, "tls", "public"), db, sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	// A stored setup whose token cannot be opened and whose certificate is not on disk.
	if err := db.SavePublicHTTPS(store.PublicHTTPS{Name: "mia.dedyn.io", Zone: "dedyn.io", Provider: "desec", Issuer: publiccert.LetsEncrypt, LANIP: "192.168.1.20", Token: []byte("not sealed with this key")}); err != nil {
		t.Fatal(err)
	}
	if err := public.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded; the test needs a setup that cannot be resumed")
	}

	configure(t, db)
	ensureHTTPS(db, certs, time.Now(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !certs.Ready() {
		t.Fatal("the own CA was not created; the box would serve plain HTTP")
	}
	requireOwnCAHandshake(t, public, certs, "mia.dedyn.io")
	if st := public.Status(); !st.Warning {
		t.Errorf("no warning after the fallback: %+v", st)
	}
}

// requireOwnCAHandshake connects to a TLS listener configured like the box and
// checks that the certificate comes from the own CA and covers name.
func requireOwnCAHandshake(t *testing.T, public *publiccert.Manager, certs *pki.Store, name string) {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certs.CAPEM()) {
		t.Fatal("no CA certificate")
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig(public, certs))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _ = c.(*tls.Conn).Handshake() }()
		}
	}()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", ln.Addr().String(), &tls.Config{ServerName: name, RootCAs: roots})
	if err != nil {
		t.Fatalf("handshake for %s: %v", name, err)
	}
	conn.Close()
}

// Decision Q27, "obtained" case: the DNS token opens, but the certificate is
// neither on disk nor obtainable (the CA cannot be reached). Start succeeds and
// the manager is ready, yet it has no certificate: the own CA must serve.
func TestPublicSetupWithoutObtainableCertificateFallsBackToTheOwnCA(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	certs, err := pki.Open(filepath.Join(dir, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secret.Open(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL + "/directory"
	down.Close() // nothing listens there any more
	public, err := publiccert.New(filepath.Join(dir, "tls", "public"), db, sealer,
		publiccert.WithDirectories(map[string]string{publiccert.LetsEncrypt: downURL}))
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	token, err := sealer.Seal([]byte("desec-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SavePublicHTTPS(store.PublicHTTPS{Name: "mia.dedyn.io", Zone: "dedyn.io", Provider: "desec", Issuer: publiccert.LetsEncrypt, LANIP: "192.168.1.20", Token: token}); err != nil {
		t.Fatal(err)
	}
	if err := public.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !public.Ready() {
		t.Fatal("the test needs a manager that is ready but has no certificate")
	}
	if public.HasCertificate() {
		t.Fatal("a manager without a certificate claims to have one")
	}

	configure(t, db)
	ensureHTTPS(db, certs, time.Now(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !certs.Ready() {
		t.Fatal("the own CA was not created; every handshake would fail")
	}
	requireOwnCAHandshake(t, public, certs, "mia.dedyn.io")
	waitFor(t, "the Q27 warning", func() bool { return public.Status().Warning })
}

// configure completes the first-run setup in db: a parent with a confirmed TOTP.
func configure(t *testing.T, db *store.DB) {
	t.Helper()
	h, err := db.CreateHousehold(store.Household{Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAccount(store.Account{PersonID: p.ID, Username: "mama", PasswordHash: "x", TOTPSecret: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmTOTP(p.ID); err != nil {
		t.Fatal(err)
	}
	if !db.Configured() {
		t.Fatal("the test needs a configured box")
	}
}

// waitFor polls cond until it holds; what it waits for happens in the background.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Before setup is complete the wizard's own-CA choice provisions the CA for
// the address the browser used: one made earlier for the public name would
// make that choice fail after a restart during the name check.
func TestOwnCAIsNotCreatedBeforeSetupIsComplete(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	certs, err := pki.Open(filepath.Join(dir, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SavePublicHTTPS(store.PublicHTTPS{Name: "mia.dedyn.io", Zone: "dedyn.io", Provider: "desec", Issuer: publiccert.LetsEncrypt, LANIP: "192.168.1.20", Token: []byte("x")}); err != nil {
		t.Fatal(err)
	}

	ensureHTTPS(db, certs, time.Now(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if certs.Ready() {
		t.Fatal("the own CA was created for the public name before setup was complete")
	}
}

func TestResetOTPCommandResetsTheAccountAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	cfg := config{DataDir: dir, SecretKeyFile: filepath.Join(dir, "secret.key")}
	db, _, err := openDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := db.CreateHousehold(store.Household{Name: "Family"})
	mama, _ := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent})
	_ = db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "h", TOTPSecret: "S"})
	_ = db.ConfirmTOTP(mama.ID)
	_ = db.ReplaceRecoveryCodes(mama.ID, []string{"x"})
	_ = db.CreateSession("tok", mama.ID, "", time.Now(), time.Now().Add(time.Hour))
	db.Close()

	var logs strings.Builder
	if err := resetOTP(cfg, "MAMA", slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "one-time codes reset") || !strings.Contains(logs.String(), "account=mama") {
		t.Errorf("reset not logged: %q", logs.String())
	}
	db, _, err = openDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	acc, _ := db.AccountByPerson(mama.ID)
	if acc.TOTPConfirmed || acc.TOTPSecret != "" {
		t.Errorf("account after reset = %+v", acc)
	}
	if codes, _ := db.UnusedRecoveryCodes(mama.ID); len(codes) != 0 {
		t.Error("recovery codes kept")
	}
	if _, err := db.SessionPerson("tok", time.Now()); err == nil {
		t.Error("browser session kept")
	}
	if err := resetOTP(cfg, "nobody", slog.New(slog.NewTextHandler(&logs, nil))); err == nil {
		t.Error("unknown account accepted")
	}
}

// docker compose logs shows only what the box's main process (PID 1) writes,
// so a reset run by `docker compose exec` must reach its stdout too.
func TestCommandLogReachesTheBoxMainProcessOutput(t *testing.T) {
	var stderr strings.Builder
	pid1 := filepath.Join(t.TempDir(), "stdout")
	if err := os.WriteFile(pid1, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	commandLogger(&stderr, pid1, 4711).Info("one-time codes reset", "account", "mama")
	got, _ := os.ReadFile(pid1)
	if !strings.Contains(string(got), "account=mama") {
		t.Errorf("box main process output = %q", got)
	}
	if !strings.Contains(stderr.String(), "account=mama") {
		t.Errorf("stderr = %q", stderr.String())
	}

	// Without a reachable main process output, stderr alone still works.
	stderr.Reset()
	commandLogger(&stderr, filepath.Join(t.TempDir(), "missing"), 4711).Info("x", "k", "v")
	if !strings.Contains(stderr.String(), "k=v") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// The weather service reads the home through homeOf: no household is
// weather.ErrNoHome, a household whose home has no location yet reports (0, 0)
// and one with a picked place reports its coordinates. Nothing is looked up by
// name any more (ADR 0021).
func TestHomeOfAdaptsTheHouseholdForTheWeather(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := homeOf{db}
	if _, _, err := h.Home(); !errors.Is(err, weather.ErrNoHome) {
		t.Fatalf("without a household: %v", err)
	}
	hh, err := db.CreateHousehold(store.Household{Name: "Home", HomeName: "Pasing", Region: "DE-BY"})
	if err != nil {
		t.Fatal(err)
	}
	if lat, lon, err := h.Home(); err != nil || lat != 0 || lon != 0 {
		t.Fatalf("home without a place = %v %v %v", lat, lon, err)
	}
	if err := setHome(db, hh, "Pasing", 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
	if lat, lon, err := h.Home(); err != nil || lat != 48.15 || lon != 11.46 {
		t.Fatalf("home with a place = %v %v %v", lat, lon, err)
	}
}

// ADR 0018: the host script asks the running box for an online backup of its
// database with `hejmo backup <file>` (docker exec), before it updates.
func TestBackupCommandCopiesTheLiveDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := loadConfig(func(k string) string { return map[string]string{"HEJMO_DATA_DIR": dir}[k] })
	live, err := store.Open(filepath.Join(dir, "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.CreateHousehold(store.Household{Name: "Familie"}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup", "pre-update.db")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backup(cfg, dest); err != nil {
		t.Fatal(err)
	}
	cp, err := store.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if h, err := cp.HomeHousehold(); err != nil || h.Name != "Familie" {
		t.Fatalf("backup content: %+v %v", h, err)
	}
	if err := backup(cfg, ""); err == nil {
		t.Fatal("backup without a target succeeded")
	}
}

// The box offers auto-update only where the install script's host updater runs.
func TestUpdateDirIsSetOnlyWhenAHostUpdaterManagesTheBox(t *testing.T) {
	plain := loadConfig(func(string) string { return "" })
	if plain.UpdateDir != "" {
		t.Fatalf("plain Compose install has UpdateDir %q", plain.UpdateDir)
	}
	managed := loadConfig(func(k string) string { return map[string]string{"HEJMO_HOST_UPDATER": "1", "HEJMO_DATA_DIR": "/d"}[k] })
	if managed.UpdateDir != "/d" {
		t.Fatalf("managed UpdateDir = %q, want the data dir", managed.UpdateDir)
	}
}

// setHome stores a home place through the settings save, keeping the rest.
func setHome(db *store.DB, h store.Household, name string, lat, lon float64) error {
	return db.SaveHouseholdSettings(h.ID, store.HouseholdSettings{DateFormat: h.DateFormat, TimeFormat: h.TimeFormat, TemperatureUnit: h.TemperatureUnit, AutoUpdate: h.AutoUpdate, MealCutoffs: h.MealCutoffs, Home: &store.Home{Name: name, Latitude: lat, Longitude: lon}})
}
