package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type opsCounters struct {
	httpFailures    atomic.Uint64
	paymentFailures atomic.Uint64
}

type failureWriter struct {
	http.ResponseWriter
	count *atomic.Uint64
	wrote bool
}

func (w *failureWriter) WriteHeader(status int) {
	if !w.wrote && status >= 200 {
		w.wrote = true
		if status >= 500 {
			w.count.Add(1)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *failureWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *failureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *App) opsMetrics(token string) http.HandlerFunc {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if strings.TrimSpace(token) == "" || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]uint64{
			"http_5xx": a.ops.httpFailures.Load(), "payment_failures": a.ops.paymentFailures.Load(),
			"task_failures": a.store.taskFailures.Load(),
		})
	}
}

func (a *App) publicStatus(configuredSupport string) http.HandlerFunc {
	support := ""
	u, err := url.Parse(configuredSupport)
	if err == nil && u.Scheme == "https" {
		support = safePurchaseURL(configuredSupport)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		status := "operational"
		if a.store.healthy(ctx) != nil {
			status = "degraded"
		}
		writeJSON(w, 200, struct {
			Status     string    `json:"status"`
			CheckedAt  time.Time `json:"checked_at"`
			SupportURL string    `json:"support_url,omitempty"`
		}{status, time.Now().UTC(), support})
	}
}
