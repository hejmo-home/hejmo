package main

import (
	"net/http"
	"net/http/httptest"
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
