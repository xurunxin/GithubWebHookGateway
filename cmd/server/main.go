package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nkit/github-webhook-relay/internal/admin"
	"github.com/nkit/github-webhook-relay/internal/config"
	"github.com/nkit/github-webhook-relay/internal/github"
	"github.com/nkit/github-webhook-relay/internal/relay"
	"github.com/nkit/github-webhook-relay/internal/storage"
	"github.com/nkit/github-webhook-relay/internal/websocket"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[main] ")

	cfg := config.Load()

	log.Printf("starting github-webhook-relay (env=%s)", cfg.AppEnv)

	db, err := storage.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate("migrations/001_init.sql"); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	relayCfg := relay.RelayConfig{
		MaxRetry:         cfg.EventMaxRetry,
		RetryInitialSecs: cfg.EventRetryInitialSecs,
		RetryMaxSecs:     cfg.EventRetryMaxSecs,
		DeliveryBatch:    cfg.EventDeliveryBatchSize,
	}

	rly := relay.New(db, relayCfg)
	rly.Start()
	defer rly.Stop()

	mux := http.NewServeMux()

	ghHandler := github.NewHandler(cfg.GitHubWebhookSecret, db, rly)
	mux.Handle("POST /webhook/github", limitBody(ghHandler, cfg.MaxBodyBytes))

	wsHandler := websocket.NewHandler(
		cfg.OpenClawAgentToken, db, rly,
		cfg.WSReadTimeout, cfg.WSWriteTimeout, cfg.WSPingInterval,
	)
	mux.HandleFunc("GET /ws/openclaw", wsHandler.ServeHTTP)

	adminHandler := admin.NewHandler(cfg.AdminToken, db, rly)
	mux.HandleFunc("GET /health", adminHandler.Health)
	mux.HandleFunc("GET /status", adminHandler.Status)
	mux.HandleFunc("GET /events", adminHandler.Events)
	mux.HandleFunc("POST /events/{id}/retry", adminHandler.RetryEvent)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}

	log.Println("server stopped")
}

func limitBody(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[http] %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
