package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

func TestHTTPSConfig(t *testing.T) {
	cfg := loadConfig(func(string) string { return "" })
	if cfg.HTTPSAddr != ":8443" || cfg.PublicHTTPSPort != "443" {
		t.Errorf("defaults: HTTPSAddr=%q PublicHTTPSPort=%q", cfg.HTTPSAddr, cfg.PublicHTTPSPort)
	}
	env := map[string]string{"HEJMO_HTTPS_ADDR": ":9443", "HEJMO_PUBLIC_HTTPS_PORT": "9443"}
	cfg = loadConfig(func(k string) string { return env[k] })
	if cfg.HTTPSAddr != ":9443" || cfg.PublicHTTPSPort != "9443" {
		t.Errorf("from env: %+v", cfg)
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
