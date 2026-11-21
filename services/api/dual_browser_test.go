//go:build browser

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This fixture is compiled only for local browser acceptance; no production test endpoints.
func TestDualExecutionBrowser(t *testing.T) {
	f := newDualFixture(t, nil)
	webDir, err := filepath.Abs("../../apps/web/dist")
	if err != nil {
		t.Fatal(err)
	}
	original := f.app.handler
	f.app.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/agent" && r.Method == "POST" {
			payload, err := io.ReadAll(io.LimitReader(r.Body, 2048))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			var output bytes.Buffer
			code := runAgentCLI(context.Background(), []string{"--api-url", f.api.URL, "--authorize-target"}, bytes.NewReader(payload), &output, f.options)
			writeJSON(w, 200, map[string]any{"exit_code": code, "output": output.String()})
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" || len(r.URL.Path) > 8 && r.URL.Path[:8] == "/assets/" {
			spaHandler(webDir).ServeHTTP(w, r)
			return
		}
		original.ServeHTTP(w, r)
	})
	f.startWorker(t)
	python := os.Getenv("BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	script, _ := filepath.Abs("../../scripts/dual_execution_browser.py")
	cmd := exec.Command(python, script, "--base", f.api.URL, "--target", f.target.URL+"/v1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal("browser acceptance failed: ", err)
	}
	if f.calls.Load() < 4 {
		t.Fatal("browser did not execute both target paths")
	}
	var count int
	if err := f.app.store.db.QueryRow("SELECT count(*) FROM runs WHERE status='succeeded'").Scan(&count); err != nil || count < 2 {
		t.Fatal("reports missing")
	}
	// Explicitly inspect each persisted run after browser refresh.
	rows, err := f.app.store.db.Query("SELECT data FROM runs")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		rows.Scan(&data)
		var run Run
		json.Unmarshal(data, &run)
		if run.Execution == nil || len(run.StageSummaries) != 1 {
			t.Fatal("browser report lost measured stages")
		}
	}
}
