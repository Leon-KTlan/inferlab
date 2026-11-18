package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestCLIDiagnosticsPersistAndDoNotLeak(t *testing.T) {
	f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"runner-test-secret-key health check private-upstream-text","code":"unsupported_parameter","param":"max_tokens"}}`)
	})
	code, out, _ := f.run(t, context.Background(), f.api.URL, true)
	if code != 1 || !strings.Contains(out, "http=400") || !strings.Contains(out, "FIRST_ERROR") || !strings.Contains(out, "param=max_tokens") || !strings.Contains(out, "count=2") || strings.Contains(out, "private-upstream-text") {
		t.Fatal("CLI omitted actionable diagnostics or leaked upstream text")
	}
	run, err := f.app.store.run(f.owner, f.task.RunID)
	if err != nil || len(run.Diagnostics) != 1 {
		t.Fatal("diagnostics not persisted")
	}
	d := run.Diagnostics[0]
	if d.HTTPStatus != 400 || d.ProviderCode != "unsupported_parameter" || d.Parameter != "max_tokens" || d.Count != 2 || d.LastAt.Before(d.FirstAt) || d.LastLatencyMS < 0 {
		t.Fatal("incorrect persisted diagnostic")
	}
	data, err := json.Marshal(run)
	if err != nil || strings.Contains(string(data), "runner-test-secret-key") || strings.Contains(string(data), "private-upstream-text") {
		t.Fatal("sensitive data reached stored run")
	}
	request, _ := http.NewRequest("GET", f.api.URL+"/api/v1/runs/"+run.RunID, nil)
	request.AddCookie(&http.Cookie{Name: "inferlab_session", Value: f.owner + "." + f.app.sign(f.owner)})
	response, err := f.api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var loaded Run
	if json.NewDecoder(response.Body).Decode(&loaded) != nil || len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].HTTPStatus != 400 {
		t.Fatal("diagnostics unavailable after reloading run")
	}
}

func TestDiagnosticStatusAndPartialFailure(t *testing.T) {
	for _, status := range []int{401, 403, 404, 408, 422, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			code, _, _ := f.run(t, context.Background(), f.api.URL, true)
			run, err := f.app.store.run(f.owner, f.task.RunID)
			if code != 1 || err != nil || len(run.Diagnostics) != 1 || run.Diagnostics[0].HTTPStatus != status || run.Diagnostics[0].Count != 2 {
				t.Fatal("HTTP status classification not preserved")
			}
			if status == 408 && run.ErrorCode != ErrTargetTimeout {
				t.Fatal("408 incorrectly classified as rate limit")
			}
		})
	}
	// Preserve execution success semantics when only part of the request sample fails.
	var calls atomic.Int64
	f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, `{"choices":[{}]}`)
	})
	code, _, _ := f.run(t, context.Background(), f.api.URL, true)
	run, err := f.app.store.run(f.owner, f.task.RunID)
	if code != 0 || err != nil || run.Status != "succeeded" || run.ErrorCount != 1 || len(run.Diagnostics) != 1 {
		t.Fatal("partial failures lost diagnostic context")
	}
}

func TestProviderDiagnosticDropsUntrustedText(t *testing.T) {
	for _, payload := range []string{
		`{"error":{"code":"sk-private-test","type":"private upstream text","param":"secret-model","message":"private message"}}`,
		`{"error":{"code":123,"message":"private message"}}`,
		`not JSON private upstream text`,
		`{"error":{"message":"` + strings.Repeat("x", 17<<10) + `","code":"unsupported_parameter"}}`,
	} {
		code, param := providerDiagnostic(strings.NewReader(payload))
		if code != "" || param != "" {
			t.Fatal("untrusted error fields survived sanitization")
		}
	}
	code, param := providerDiagnostic(strings.NewReader(`{"error":{"code":"unknown","type":"invalid_request_error","param":"model"}}`))
	if code != "invalid_request_error" || param != "model" {
		t.Fatal("safe protocol symbols lost")
	}
}

func TestDiagnosticAggregationBoundedAndConcurrent(t *testing.T) {
	var log diagnosticLog
	var wg sync.WaitGroup
	now := time.Now().UTC()
	d := RequestDiagnostic{ErrorCode: ErrHTTP4xx, HTTPStatus: 400, Count: 1, FirstAt: now, LastAt: now, LastLatencyMS: 1}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); log.record(d) }()
	}
	wg.Wait()
	if got := log.snapshot(); len(got) != 1 || got[0].Count != 50 {
		t.Fatal("concurrent errors not aggregated")
	}
	for status := 401; status < 500; status++ {
		d.HTTPStatus = status
		log.record(d)
	}
	if len(log.snapshot()) != maxDiagnostics {
		t.Fatal("unbounded diagnostics")
	}
}

func TestDiagnosticValidationRejectsUnsafeResult(t *testing.T) {
	f := newCLIFixture(t, nil)
	result := validLocalResult(f.task.RunID, f.task.Config)
	// Use the existing valid result's window while testing the diagnostic boundary.
	result.Metrics.ErrorCount = result.Metrics.CompletedCount
	result.Metrics.SuccessCount = 0
	result.Metrics.ErrorRate = 1
	for i := range result.StageSummaries {
		stage := &result.StageSummaries[i]
		stage.ErrorCount = stage.CompletedCount
		stage.SuccessCount = 0
		stage.ErrorRate = 1
		stage.SLOStatus = sloStatus(stage.Metrics, f.task.Config.SLO)
	}
	d := RequestDiagnostic{ErrorCode: ErrHTTP4xx, HTTPStatus: 400, Count: 1, FirstAt: result.StartedAt.Add(time.Millisecond), LastAt: result.StartedAt.Add(2 * time.Millisecond), LastLatencyMS: 1}
	result.Diagnostics = []RequestDiagnostic{d}
	if err := result.validateFor(f.task.Config); err != nil {
		t.Fatalf("diagnostic baseline invalid: %v", err)
	}
	for _, mutate := range []func(*RequestDiagnostic){
		func(d *RequestDiagnostic) { d.ProviderCode = "private upstream text" },
		func(d *RequestDiagnostic) { d.Parameter = "secret-model" },
		func(d *RequestDiagnostic) { d.FailureKind = "private network error" },
		func(d *RequestDiagnostic) { d.Count = result.Metrics.CompletedCount + 1 },
		func(d *RequestDiagnostic) { d.HTTPStatus = 999 },
		func(d *RequestDiagnostic) { d.LastLatencyMS = math.Inf(1) },
		func(d *RequestDiagnostic) { d.FirstAt = result.StartedAt.Add(-time.Second) },
		func(d *RequestDiagnostic) { d.LastAt = result.FinishedAt.Add(time.Second) },
	} {
		bad := d
		mutate(&bad)
		result.Diagnostics = []RequestDiagnostic{bad}
		payload, err := json.Marshal(result)
		if err != nil {
			if result.validateFor(f.task.Config) == nil {
				t.Fatal("nonfinite diagnostic accepted")
			}
			continue
		}
		request, _ := http.NewRequest("POST", f.api.URL+f.task.ResultEndpoint, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-InferLab-Runner-Token", f.task.TaskToken)
		response, err := f.api.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal("API accepted unsafe diagnostics")
		}
	}
}

func TestNetworkFailureUsesSafeCategories(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "private", Name: "private"}, "dns"},
		{x509.UnknownAuthorityError{}, "tls_certificate"},
		{syscall.ECONNREFUSED, "connection_refused"},
		{syscall.ENETUNREACH, "network_unreachable"},
		{errors.New("private upstream network text"), "connection"},
	} {
		if got := networkFailure(tc.err); got != tc.want {
			t.Fatal("unsafe or incorrect network category")
		}
	}
}
