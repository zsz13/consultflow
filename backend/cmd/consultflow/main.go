// Command consultflow runs the ConsultFlow REST API.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"consultflow/internal/api"
	"consultflow/internal/store"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check the running API and exit (for container health checks)")
	flag.Parse()
	if *healthcheck {
		os.Exit(checkHealth(getenv("ADDR", ":8081")))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := getenv("DATABASE_URL", "postgres://consultflow:consultflow@localhost:55432/consultflow?sslmode=disable")
	addr := getenv("ADDR", ":8081")

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if seeded, err := st.SeedIfEmpty(ctx, time.Now()); err != nil {
		log.Fatalf("seed: %v", err)
	} else if seeded {
		log.Print("loaded synthetic demo data")
	}

	srv := &http.Server{Addr: addr, Handler: logRequests(api.New(st, time.Now)), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("consultflow API listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// checkHealth returns 0 when the API on addr answers /api/health.
func checkHealth(addr string) int {
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://localhost" + addr + "/api/health")
	if err != nil {
		return 1
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
	})
}
