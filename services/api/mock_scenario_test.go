package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockScenarioConfig(t *testing.T) {
	for _, scenario := range []string{"", "baseline-v1", "slow-v1"} {
		config := testConfig()
		config.Target.MockScenario = scenario
		if err := config.validate(); err != nil {
			t.Fatalf("scenario %q rejected: %v", scenario, err)
		}
	}
	config := testConfig()
	config.Target.MockScenario = "unknown-v1"
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "mock_scenario") {
		t.Fatalf("unknown Mock scenario validation: %v", err)
	}
	config.Target = Target{Type: "openai_compatible", Model: "test-model", MockScenario: "baseline-v1"}
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "mock_scenario") {
		t.Fatalf("configured Mock scenario on real target validation: %v", err)
	}
	if err := validateLocalConfig(context.Background(), config, EndpointPolicy{}); err == nil || err.Error() != "LOCAL_RUNNER_TARGET_REQUIRED" {
		t.Fatalf("configured Mock scenario on local task validation: %v", err)
	}
}

func TestMockScenarioHTTPRunAndSnapshot(t *testing.T) {
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()

	runScenario := func(id string) Run {
		t.Helper()
		client := clientWithCookies()
		config := testConfig()
		config.Target.MockScenario = id
		config.Workload = Workload{ConcurrencySteps: []int{16}, StageSeconds: 2, MaxRequests: 20, MaxRPS: 50, TimeoutMS: 1500}

		var experiment Experiment
		request(t, client, http.MethodPost, server.URL+"/api/v1/experiments", config, http.StatusCreated, &experiment)
		var started Run
		request(t, client, http.MethodPost, server.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, http.StatusAccepted, &started)
		run := waitRun(t, client, server.URL, started.RunID)
		if run.Status != "succeeded" || run.CompletedCount != 20 || run.ErrorCount != 0 || run.Config.Target.MockScenario != id {
			t.Fatalf("unexpected %s run: %+v", id, run)
		}
		return run
	}

	baseline := runScenario("baseline-v1")
	slow := runScenario("slow-v1")
	if baseline.P50E2EMS < 160 || slow.P50E2EMS < 420 || baseline.P95E2EMS >= slow.P50E2EMS {
		t.Fatalf("unexpected Mock latency: baseline P50 %.2f ms, P95 %.2f ms; slow P50 %.2f ms", baseline.P50E2EMS, baseline.P95E2EMS, slow.P50E2EMS)
	}
	if code, _ := comparisonIssue(baseline, slow); code != "CONFIG_MISMATCH" {
		t.Fatalf("expected configuration mismatch between scenarios, got %q", code)
	}
}
