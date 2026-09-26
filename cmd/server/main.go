package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sokr/internal/httpapi"
	"sokr/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	token := os.Getenv("CREATE_TOKEN")
	if token == "" {
		log.Fatal("CREATE_TOKEN is empty")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is empty")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pg, err := store.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pg.Close()
	go sweep(ctx, pg)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.New(pg, token, httpapi.Page).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func sweep(ctx context.Context, pg *store.Postgres) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := pg.Sweep(c); err != nil {
				log.Printf("sweep: %v", err)
			}
			cancel()
		}
	}
}
