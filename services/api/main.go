package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	appEnv := env("APP_ENV", "local")
	secureEnv := appEnv == "staging" || appEnv == "production"
	app, err := newApp(env("DB_PATH", "data/inferlab.db"), []byte(os.Getenv("SESSION_SECRET")), secureEnv, os.Getenv("WEB_DIR"))
	if err != nil {
		log.Fatal("API initialization failed: ", err)
	}
	server := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("InferLab Mock API listening on %s", server.Addr)
	err = server.ListenAndServe()
	app.Close()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal("API server stopped: ", err)
	}
}
