// Command gantry is the Gantry monitor: collectors, storage, and web UI
// in one binary. See docs/superpowers/specs/ for the design.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/smidley/gantry/internal/config"
	"github.com/smidley/gantry/internal/fake"
	"github.com/smidley/gantry/internal/server"
	"github.com/smidley/gantry/internal/store"
)

var version = "dev" // set via -ldflags at build

func main() {
	hc := flag.Bool("healthcheck", false, "probe the local healthz endpoint and exit")
	flag.Parse()

	if *hc {
		if err := healthcheck(os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, version); err != nil {
		log.Fatal(err)
	}
}

// envOnly resolves keys that must work before the store exists.
func envOnly(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, getenv func(string) string, ver string) error {
	dbPath := envOnly(getenv, "GANTRY_DB_PATH", "/config/gantry.db")

	st, err := store.Open(dbPath, nil)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg := config.New(st, getenv)
	port := cfg.Int("port", 8380)

	var wg sync.WaitGroup

	if cfg.Bool("fake_data", false) {
		log.Println("fake data mode: synthesizing a demo fleet")
		wg.Add(1)
		go func() {
			defer wg.Done()
			fake.New(st, time.Now().UnixNano()).Run(runCtx, 2*time.Second, nil)
		}()
	}

	// Maintenance: flush every minute; downsample + prune every 10 minutes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		flush := time.NewTicker(60 * time.Second)
		deep := time.NewTicker(10 * time.Minute)
		defer flush.Stop()
		defer deep.Stop()
		ret := store.DefaultRetention()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-flush.C:
				if _, err := st.FlushMinutes(time.Now()); err != nil {
					log.Println("flush:", err)
				}
			case <-deep.C:
				if _, err := st.FlushMinutes(time.Now()); err != nil {
					log.Println("flush:", err)
				}
				if err := st.DownsampleOnce(time.Now()); err != nil {
					log.Println("downsample:", err)
				}
				if err := st.PruneOnce(time.Now(), ret); err != nil {
					log.Println("prune:", err)
				}
			}
		}
	}()

	log.Printf("gantry %s listening on :%d", ver, port)
	err = server.New(server.Options{
		Port:    port,
		Version: ver,
		Store:   st,
		Started: time.Now(),
	}).ListenAndServe(runCtx)
	cancel()
	wg.Wait()
	return err
}

func healthcheck(getenv func(string) string) error {
	port := envOnly(getenv, "GANTRY_PORT", "8380")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
