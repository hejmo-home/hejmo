// Command hejmo runs the Hejmo box.
//
// Usage:
//
//	hejmo              start the box
//	hejmo healthcheck  exit 0 if the local box answers /healthz (for Docker)
package main

import (
	"context"
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

	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

type config struct {
	Addr    string // listen address, HEJMO_ADDR
	DataDir string // persistent data, HEJMO_DATA_DIR
}

func loadConfig(getenv func(string) string) config {
	cfg := config{Addr: ":8080", DataDir: "/data"}
	if v := getenv("HEJMO_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("HEJMO_DATA_DIR"); v != "" {
		cfg.DataDir = v
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

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "hejmo.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(server.Options{DB: db, Logger: logger}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		logger.Info("box listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
