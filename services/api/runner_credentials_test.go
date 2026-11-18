package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func claimCredential(t *testing.T, f *cliFixture, token string, target Target) int {
	t.Helper()
	data, err := json.Marshal(map[string]any{"target": target})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", f.api.URL+"/api/v1/runner-tasks/"+f.task.RunID+"/credential", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-InferLab-Runner-Token", token)
	response, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("credential response must not be cached")
	}
	io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestRunnerCLIClaimsBrowserKey(t *testing.T) {
	f := newCLIFixture(t, nil, "runner-test-secret-key")
	t.Setenv("OPENAI_API_KEY", "")
	if !f.task.CredentialAvailable {
		t.Fatal("task must advertise credential handoff")
	}
	data, err := json.Marshal(f.task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "runner-test-secret-key") {
		t.Fatal("key leaked into task file")
	}
	code, _, _ := f.run(t, context.Background(), f.api.URL, false)
	if code != 2 {
		t.Fatal("authorization required before claiming key")
	}
	code, _, _ = f.run(t, context.Background(), f.api.URL, true)
	if code != 0 || f.targetCalls.Load() != 2 {
		t.Fatal("browser key did not execute and upload")
	}
	if _, ok := f.app.credentials.take(f.task.RunID); ok {
		t.Fatal("credential survived execution")
	}
	for _, table := range []string{"runs", "experiments", "events"} {
		rows, err := f.app.store.db.Query("SELECT data FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(value, "runner-test-secret-key") {
				t.Fatal("key leaked into persisted records")
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

func TestRunnerCredentialIsolationAndExpiry(t *testing.T) {
	f := newCLIFixture(t, nil, "runner-test-secret-key")
	if claimCredential(t, f, "invalid", f.task.Config.Target) != 403 {
		t.Fatal("wrong token accepted")
	}
	other := f.task.Config.Target
	other.Endpoint = "https://different.example/v1"
	if claimCredential(t, f, f.task.TaskToken, other) != 400 {
		t.Fatal("changed target accepted")
	}
	if claimCredential(t, f, f.task.TaskToken, f.task.Config.Target) != 200 {
		t.Fatal("valid claim failed")
	}
	if claimCredential(t, f, f.task.TaskToken, f.task.Config.Target) != 410 {
		t.Fatal("credential can be claimed twice")
	}

	f = newCLIFixture(t, nil, "runner-test-secret-key")
	f.app.credentials.mu.Lock()
	entry := f.app.credentials.entries[f.task.RunID]
	entry.expires = time.Now().Add(-time.Second)
	f.app.credentials.entries[f.task.RunID] = entry
	f.app.credentials.mu.Unlock()
	code, _, _ := f.run(t, context.Background(), f.api.URL, true)
	if code != 1 || f.targetCalls.Load() != 0 {
		t.Fatal("expired credential must fail before target request")
	}
}

func TestRunnerCredentialCanceledAndCapacity(t *testing.T) {
	f := newCLIFixture(t, nil, "runner-test-secret-key")
	req, _ := http.NewRequest("POST", f.api.URL+"/api/v1/runs/"+f.task.RunID+"/cancel", nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: f.owner + "." + f.app.sign(f.owner)})
	response, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatal("cancel failed")
	}
	if _, ok := f.app.credentials.take(f.task.RunID); ok {
		t.Fatal("canceled credential retained")
	}
	if claimCredential(t, f, f.task.TaskToken, f.task.Config.Target) != 409 {
		t.Fatal("canceled task credential exposed")
	}
	var credentials runnerCredentials
	defer credentials.close()
	for i := 0; i < 64; i++ {
		if !credentials.put(newID("run"), "test-only") {
			t.Fatal("unexpected credential limit")
		}
	}
	if credentials.put("overflow", "test-only") {
		t.Fatal("unbounded credential storage")
	}
	credentials.close()
	if len(credentials.entries) != 0 {
		t.Fatal("close must remove pending keys")
	}
}

func TestRunnerCredentialClaimAtomic(t *testing.T) {
	var credentials runnerCredentials
	defer credentials.close()
	credentials.put("run", "test-only")
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, ok := credentials.take("run"); results <- ok }()
	}
	wg.Wait()
	close(results)
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("credential must have exactly one consumer")
	}
}

func TestRunnerCredentialRejectsRedirectAndChangedTask(t *testing.T) {
	f := newCLIFixture(t, nil, "runner-test-secret-key")
	f.task.Config.Target.Model = "different-model"
	f.saveTask(t)
	code, _, _ := f.run(t, context.Background(), f.api.URL, true)
	if code != 1 || f.targetCalls.Load() != 0 {
		t.Fatal("modified task must not receive key or execute")
	}
	if _, ok := f.app.credentials.take(f.task.RunID); !ok {
		t.Fatal("mismatched task consumed key")
	}
	trapCalls := 0
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapCalls++ }))
	defer trap.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	f.options.apiTLSConfig.RootCAs.AddCert(redirect.Certificate())
	code, _, _ = f.run(t, context.Background(), redirect.URL, true)
	if code != 1 || trapCalls != 0 || f.targetCalls.Load() != 0 {
		t.Fatal("credential claim followed a redirect or executed without key")
	}
}
