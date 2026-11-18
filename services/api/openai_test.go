package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointSSRFPolicy(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/v1",
		"https://127.0.0.1/v1",
		"https://169.254.169.254/latest/meta-data",
		"https://100.100.100.200/latest/meta-data",
		"https://[::ffff:100.100.100.200]/latest/meta-data",
		"https://[::1]/v1",
		"https://example.com/v1?secret=1",
	} {
		if _, err := validateEndpoint(context.Background(), raw, EndpointPolicy{}); err == nil {
			t.Errorf("endpoint %q was accepted", raw)
		}
	}
	if _, err := validateEndpoint(context.Background(), "https://127.0.0.1/v1", EndpointPolicy{AllowLoopback: true}); err != nil {
		t.Fatalf("explicit loopback test policy rejected endpoint: %v", err)
	}
}

func TestSharedAddressSpaceBlockedAtConnection(t *testing.T) {
	for _, address := range []string{"100.64.0.1:443", "100.100.100.200:443", "100.127.255.254:443", "[::ffff:100.100.100.200]:443"} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		connection, err := safeDialContext(false)(ctx, "tcp", address)
		cancel()
		if connection != nil {
			connection.Close()
		}
		if err == nil || !strings.Contains(err.Error(), ErrSSRFBlocked) {
			t.Fatalf("shared address was not rejected before dialing: %s", address)
		}
	}
}

func TestOpenAICompatibleAdapterAndErrorClassification(t *testing.T) {
	var authorization string
	var requestBody map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	target := Target{Type: "openai_compatible", Endpoint: server.URL + "/v1", Model: "test-model"}
	adapter, err := newOpenAICompatibleAdapterWithPolicy(target, "test-key", EndpointPolicy{AllowLoopback: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if code := adapter.Request(context.Background()); code != "" {
		t.Fatalf("successful response returned %s", code)
	}
	if authorization != "Bearer test-key" || requestBody["model"] != "test-model" || requestBody["max_tokens"] != float64(32) {
		t.Fatalf("request was not OpenAI-compatible: auth=%q body=%v", authorization, requestBody)
	}

	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, ErrAuthFailed},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusBadRequest, ErrHTTP4xx},
		{http.StatusBadGateway, ErrHTTP5xx},
	} {
		status := tc.status
		caseServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "do-not-return", status) }))
		caseTarget := Target{Type: "openai_compatible", Endpoint: caseServer.URL + "/v1", Model: "test-model"}
		caseAdapter, err := newOpenAICompatibleAdapterWithPolicy(caseTarget, "test-key", EndpointPolicy{AllowLoopback: true, HTTPClient: caseServer.Client()})
		if err != nil {
			caseServer.Close()
			t.Fatal(err)
		}
		if got := caseAdapter.Request(context.Background()); got != tc.code {
			t.Errorf("status %d returned %s, want %s", status, got, tc.code)
		}
		caseServer.Close()
	}

	timeoutServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer timeoutServer.Close()
	timeoutTarget := Target{Type: "openai_compatible", Endpoint: timeoutServer.URL + "/v1", Model: "test-model"}
	timeoutAdapter, err := newOpenAICompatibleAdapterWithPolicy(timeoutTarget, "test-key", EndpointPolicy{AllowLoopback: true, HTTPClient: timeoutServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got := timeoutAdapter.Request(ctx); got != ErrTargetTimeout {
		t.Fatalf("timeout returned %s", got)
	}

	redirectServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/v1", http.StatusTemporaryRedirect)
	}))
	defer redirectServer.Close()
	redirectTarget := Target{Type: "openai_compatible", Endpoint: redirectServer.URL + "/v1", Model: "test-model"}
	redirectAdapter, err := newOpenAICompatibleAdapterWithPolicy(redirectTarget, "test-key", EndpointPolicy{AllowLoopback: true, HTTPClient: redirectServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if got := redirectAdapter.Request(context.Background()); got != ErrRedirectBlocked {
		t.Fatalf("redirect returned %s", got)
	}
	for _, code := range []string{ErrAuthFailed, ErrRateLimited, ErrHTTP4xx, ErrHTTP5xx, ErrTargetTimeout, ErrRedirectBlocked} {
		if strings.Contains(code, "test-key") {
			t.Fatalf("classified error contains credential: %s", code)
		}
	}
}

func TestLocalRunnerFakeOpenAIProtocol(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	config := testConfig()
	config.Name = "Local runner test"
	config.Target = Target{Type: "openai_compatible", Endpoint: server.URL + "/v1", Model: "test-model"}
	config.Workload.ConcurrencySteps = []int{1}
	config.Workload.StageSeconds = 1
	config.Workload.MaxRequests = 2
	config.Workload.MaxRPS = 10
	task := RunnerTask{TaskID: "task_1", RunID: "run_1", ExperimentID: "exp_1", SourceType: "openai_compatible", Config: config}
	adapter, err := newOpenAICompatibleAdapterWithPolicy(config.Target, "test-key", EndpointPolicy{AllowLoopback: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeLocalTaskWithAdapter(context.Background(), task, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != task.RunID || result.Status != "succeeded" || result.Metrics.RequestCount != 2 || result.Metrics.CompletedCount != 2 {
		t.Fatalf("unexpected local runner result: %+v", result)
	}
}
