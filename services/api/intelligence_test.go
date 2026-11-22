package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func intelligenceTestApp(t *testing.T) (*App, *httptest.Server, *http.Client) {
	t.Helper()
	a, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	a.intelligence.interval = time.Millisecond
	server := httptest.NewServer(a.handler)
	t.Cleanup(func() { server.Close(); a.Close() })
	return a, server, clientWithCookies()
}

func intelligenceInput(profile string) map[string]any {
	return map[string]any{"name": "能力探针", "target": Target{Type: "mock", Model: "fixture"}, "mock_profile": profile, "repetitions": 1}
}

func waitIntelligence(t *testing.T, a *App, id string) Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		owner, err := a.store.runOwner(id)
		if err != nil {
			t.Fatal(err)
		}
		run, err := a.store.run(owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if run.terminal() {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("intelligence run did not finish")
	return Run{}
}

func TestIntelligenceHTTPLifecycleAndIsolation(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	var created struct {
		Run Run `json:"run"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", intelligenceInput("reference"), 202, &created)
	if created.Run.Config.Intelligence.Repetitions != 1 || created.Run.Config.Workload.MaxRequests != 10 {
		t.Fatal("snapshot or budget lost")
	}
	done := waitIntelligence(t, a, created.Run.RunID)
	if done.Status != "succeeded" || done.IntelligenceReport.Score != 100 || !done.IntelligenceReport.Complete {
		t.Fatalf("unexpected report: %+v", done)
	}
	stranger := clientWithCookies()
	request(t, stranger, "GET", server.URL+"/api/v1/intelligence/runs/"+done.RunID, nil, 404, nil)
	var list struct {
		Runs []Run `json:"runs"`
	}
	request(t, stranger, "GET", server.URL+"/api/v1/intelligence/runs", nil, 200, &list)
	if len(list.Runs) != 0 {
		t.Fatal("cross-owner history leak")
	}
	request(t, client, "GET", server.URL+"/api/v1/intelligence/runs", nil, 200, &list)
	if len(list.Runs) != 1 {
		t.Fatal("missing history")
	}
	var repeated struct {
		Run Run `json:"run"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+done.RunID+"/rerun", nil, 202, &repeated)
	rerun := waitIntelligence(t, a, repeated.Run.RunID)
	if rerun.ExperimentID != done.ExperimentID || rerun.IntelligenceReport.Score != 100 {
		t.Fatal("rerun did not preserve snapshot")
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", intelligenceInput("degraded"), 202, &created)
	if got := waitIntelligence(t, a, created.Run.RunID).IntelligenceReport.Score; got != 40 {
		t.Fatalf("degraded score %v", got)
	}
}

func TestIntelligenceQuotaCancellationAndDefault(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	a.intelligence.interval = 10 * time.Millisecond
	var created struct {
		Run Run `json:"run"`
	}
	input := intelligenceInput("reference")
	delete(input, "repetitions")
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
	if created.Run.Config.Intelligence.Repetitions != 2 || created.Run.Config.Workload.MaxRequests != 20 {
		t.Fatal("default budget must be 20")
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 429, nil)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+created.Run.RunID+"/cancel", nil, 202, nil)
	canceled := waitIntelligence(t, a, created.Run.RunID)
	if canceled.Status != "canceled" || canceled.IntelligenceReport.Complete {
		t.Fatalf("cancel lost: %+v", canceled)
	}
	owner, _ := a.store.runOwner(canceled.RunID)
	quota, err := a.store.quota(owner)
	if err != nil || quota.Reserved != 20 {
		t.Fatalf("reserved budget: %+v %v", quota, err)
	}
	a.store.dailyLimit = 20
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 429, nil)
}

func intelligenceValidResult(config Config, id string) IntelligenceRunnerResult {
	start := time.Now().UTC()
	finish := start.Add(time.Millisecond)
	result := IntelligenceRunnerResult{RunID: id, Status: "succeeded", StartedAt: &start, FinishedAt: &finish, Outcomes: []IntelligenceOutcome{}}
	for round := 1; round <= config.Intelligence.Repetitions; round++ {
		for _, probe := range intelligenceProbes() {
			result.Outcomes = append(result.Outcomes, IntelligenceOutcome{ProbeID: probe.ID, Repetition: round, Passed: true})
		}
	}
	return result
}

func intelligenceSubmit(t *testing.T, server *httptest.Server, client *http.Client, id, token string, result any, want int) {
	t.Helper()
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/intelligence/runs/"+id+"/result", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-InferLab-Runner-Token", token)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		var payload any
		json.NewDecoder(response.Body).Decode(&payload)
		t.Fatalf("result status %d want %d: %v", response.StatusCode, want, payload)
	}
}

func TestIntelligenceRunnerResultTokenValidationAndIdempotence(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	input := intelligenceInput("")
	input["target"] = Target{Type: "openai_compatible", Endpoint: "https://8.8.8.8/v1/", Model: "fixture"}
	input["authorized"] = true
	var created struct {
		Run  Run        `json:"run"`
		Task RunnerTask `json:"task"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
	if created.Task.ResultEndpoint != "/api/v1/intelligence/runs/"+created.Run.RunID+"/result" || created.Run.Config.Target.Endpoint != "https://8.8.8.8/v1" {
		t.Fatal("invalid task endpoint")
	}
	result := intelligenceValidResult(created.Run.Config, created.Run.RunID)
	intelligenceSubmit(t, server, client, result.RunID, "", result, 403)
	invalid := result
	invalid.Outcomes = append([]IntelligenceOutcome(nil), result.Outcomes...)
	invalid.Outcomes[1] = invalid.Outcomes[0]
	intelligenceSubmit(t, server, client, result.RunID, created.Task.TaskToken, invalid, 400)
	intelligenceSubmit(t, server, clientWithCookies(), result.RunID, created.Task.TaskToken, result, 202)
	intelligenceSubmit(t, server, client, result.RunID, created.Task.TaskToken, result, 409)
	final := waitIntelligence(t, a, result.RunID)
	if final.IntelligenceReport.Score != 100 || final.CompletedCount != 10 || final.SuccessCount != 10 || final.ErrorCount != 0 || final.P95E2EMS != 0 {
		t.Fatal("server did not recompute score")
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+result.RunID+"/rerun", map[string]any{}, 400, nil)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+result.RunID+"/rerun", map[string]any{"authorized": true}, 202, &created)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+created.Run.RunID+"/cancel", nil, 202, nil)
	result = intelligenceValidResult(created.Run.Config, created.Run.RunID)
	intelligenceSubmit(t, server, client, result.RunID, created.Task.TaskToken, result, 409)
}

func TestIntelligenceResultBoundaries(t *testing.T) {
	repetitions := 1
	config, err := intelligenceConfig(intelligenceCreateRequest{Name: "test", Target: Target{Type: "mock"}, Repetitions: &repetitions})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*IntelligenceRunnerResult)
	}{
		{"missing", func(r *IntelligenceRunnerResult) { r.Outcomes = r.Outcomes[:9] }},
		{"unknown", func(r *IntelligenceRunnerResult) { r.Outcomes[0].ProbeID = "unknown" }},
		{"round", func(r *IntelligenceRunnerResult) { r.Outcomes[0].Repetition = 0 }},
		{"nan", func(r *IntelligenceRunnerResult) { r.Outcomes[0].LatencyMS = math.NaN() }},
		{"error", func(r *IntelligenceRunnerResult) { r.Outcomes[0].ErrorCode = "SECRET_TEXT" }},
		{"passed error", func(r *IntelligenceRunnerResult) { r.Outcomes[0].ErrorCode = ErrTargetTimeout }},
		{"long window", func(r *IntelligenceRunnerResult) { end := r.StartedAt.Add(122 * time.Second); r.FinishedAt = &end }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := intelligenceValidResult(config, "run")
			test.mutate(&result)
			if result.validateFor(config) == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestIntelligenceRadarBaselineAndFiltering(t *testing.T) {
	repetitions := 1
	config, err := intelligenceConfig(intelligenceCreateRequest{Name: "radar", Target: Target{Type: "mock", Model: "fixture"}, Repetitions: &repetitions})
	if err != nil {
		t.Fatal(err)
	}
	runs := []Run{}
	for i := 0; i < 4; i++ {
		result := intelligenceValidResult(config, "run")
		if i == 0 {
			for j := 4; j < len(result.Outcomes); j++ {
				result.Outcomes[j].Passed = false
			}
		}
		report := summarizeIntelligence(*config.Intelligence, result.Outcomes)
		runs = append(runs, Run{RunID: string(rune('a' + i)), Config: config, SourceType: "mock", Status: "succeeded", CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour), IntelligenceReport: &report})
	}
	groups := intelligenceRadar(runs)
	if len(groups) != 1 || groups[0].Signal != "review" || *groups[0].BaselineScore != 100 || *groups[0].Delta != -60 {
		t.Fatalf("wrong radar: %+v", groups)
	}
	runs[1].IntelligenceReport.ErrorCount = 1
	if intelligenceRadar(runs)[0].Signal != "insufficient_data" {
		t.Fatal("transport error entered baseline")
	}
	runs[1].IntelligenceReport.ErrorCount = 0
	runs[0].Status = "failed"
	if intelligenceRadar(runs)[0].Signal != "insufficient_data" {
		t.Fatal("failed latest produced signal")
	}
	if groups := intelligenceRadar(nil); groups == nil || len(groups) != 0 {
		t.Fatal("empty groups must be array")
	}
}

func TestIntelligenceInputAuthorizationAndSSRF(t *testing.T) {
	_, server, client := intelligenceTestApp(t)
	for _, repetitions := range []int{-1, 0, 4} {
		input := intelligenceInput("reference")
		input["repetitions"] = repetitions
		request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 400, nil)
	}
	for _, endpoint := range []string{"http://8.8.8.8/v1", "https://127.0.0.1/v1", "https://169.254.169.254/v1", "https://user:secret@8.8.8.8/v1"} {
		input := intelligenceInput("")
		input["target"] = Target{Type: "openai_compatible", Endpoint: endpoint, Model: "fixture"}
		input["authorized"] = true
		request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 400, nil)
	}
	input := intelligenceInput("")
	input["target"] = Target{Type: "openai_compatible", Endpoint: "https://8.8.8.8/v1", Model: "fixture"}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 400, nil)
	var radar struct {
		Groups []IntelligenceRadarGroup `json:"groups"`
	}
	request(t, client, "GET", server.URL+"/api/v1/intelligence/radar", nil, 200, &radar)
	if radar.Groups == nil || len(radar.Groups) != 0 {
		t.Fatal("empty radar must be an array")
	}
}

func TestIntelligenceAndPerformanceShareActiveLimit(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	a.intelligence.interval = time.Second
	var experiment Experiment
	request(t, client, "POST", server.URL+"/api/v1/experiments", testConfig(), 201, &experiment)
	var created struct {
		Run Run `json:"run"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", intelligenceInput("reference"), 202, &created)
	request(t, client, "POST", server.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 429, nil)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+created.Run.RunID+"/cancel", nil, 202, nil)
	waitIntelligence(t, a, created.Run.RunID)
	var performance Run
	request(t, client, "POST", server.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 202, &performance)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", intelligenceInput("reference"), 429, nil)
	request(t, client, "POST", server.URL+"/api/v1/runs/"+performance.RunID+"/cancel", nil, 202, nil)
}

func TestIntelligencePerformanceRoutesReject(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	input := intelligenceInput("reference")
	input["repetitions"] = 0
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 400, nil)
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("explicit zero created run: count=%d err=%v", count, err)
	}
	delete(input, "repetitions")
	var created struct {
		Run Run `json:"run"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
	capability := waitIntelligence(t, a, created.Run.RunID)
	if capability.Config.Intelligence.Repetitions != 2 || capability.IntelligenceReport.Total != 20 {
		t.Fatal("omitted repetitions did not default to two")
	}
	owner, err := a.store.runOwner(capability.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var experiment Experiment
	request(t, client, "POST", server.URL+"/api/v1/experiments", testConfig(), 201, &experiment)
	performance := Run{RunID: newID("run"), ExperimentID: experiment.ExperimentID, Status: "succeeded", SourceType: "mock", ResultKind: "demo", Config: experiment.Config, CreatedAt: time.Now().UTC()}
	if err := a.store.reserveRun(owner, performance); err != nil {
		t.Fatal(err)
	}
	paths := []struct{ name, method, path string }{
		{"start", "POST", "/api/v1/experiments/" + capability.ExperimentID + "/runs"},
		{"runner task", "POST", "/api/v1/experiments/" + capability.ExperimentID + "/runner-tasks"},
		{"report", "GET", "/api/v1/runs/" + capability.RunID + "/report"},
		{"rerun", "POST", "/api/v1/runs/" + capability.RunID + "/rerun"},
		{"share", "POST", "/api/v1/runs/" + capability.RunID + "/share"},
		{"compare capability candidate", "GET", "/api/v1/reports/" + capability.RunID + "/compare?baseline_run_id=" + performance.RunID},
		{"compare capability baseline", "GET", "/api/v1/reports/" + performance.RunID + "/compare?baseline_run_id=" + capability.RunID},
	}
	for _, route := range paths {
		t.Run(route.name, func(t *testing.T) {
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			request(t, client, route.method, server.URL+route.path, nil, 409, &response)
			if response.Error.Code != "INTELLIGENCE_ROUTE_REQUIRED" {
				t.Fatalf("wrong guard: %s", response.Error.Code)
			}
			if err := a.store.db.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 2 {
				t.Fatalf("rejected route created run: count=%d err=%v", count, err)
			}
		})
	}
	var visibleRuns struct {
		Runs []Run `json:"runs"`
	}
	request(t, client, "GET", server.URL+"/api/v1/runs", nil, 200, &visibleRuns)
	if len(visibleRuns.Runs) != 1 || visibleRuns.Runs[0].RunID != performance.RunID {
		t.Fatal("performance history did not isolate capability run")
	}
	var visibleExperiments struct {
		Experiments []Experiment `json:"experiments"`
	}
	request(t, client, "GET", server.URL+"/api/v1/experiments", nil, 200, &visibleExperiments)
	if len(visibleExperiments.Experiments) != 1 || visibleExperiments.Experiments[0].ExperimentID != experiment.ExperimentID {
		t.Fatal("performance templates did not isolate capability experiment")
	}
	if err := a.store.db.QueryRow("SELECT count(*) FROM report_shares").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected share created public token: count=%d err=%v", count, err)
	}
}

func TestIntelligenceRejectedCreationRollsBackExperiment(t *testing.T) {
	a, server, client := intelligenceTestApp(t)
	a.intelligence.interval = time.Second
	var created struct {
		Run Run `json:"run"`
	}
	input := intelligenceInput("reference")
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
	assertCounts := func(experiments, runs int) {
		t.Helper()
		var experimentCount, runCount int
		if err := a.store.db.QueryRow("SELECT count(*) FROM experiments").Scan(&experimentCount); err != nil {
			t.Fatal(err)
		}
		if err := a.store.db.QueryRow("SELECT count(*) FROM runs").Scan(&runCount); err != nil {
			t.Fatal(err)
		}
		if experimentCount != experiments || runCount != runs {
			t.Fatalf("record counts: experiments=%d runs=%d, want %d/%d", experimentCount, runCount, experiments, runs)
		}
	}
	assertCounts(1, 1)
	var rejection struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 429, &rejection)
	if rejection.Error.Code != "RUN_QUOTA_EXCEEDED" {
		t.Fatalf("wrong active quota error: %s", rejection.Error.Code)
	}
	assertCounts(1, 1)
	firstID := created.Run.RunID
	experimentID := created.Run.ExperimentID
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+firstID+"/cancel", nil, 202, nil)
	waitIntelligence(t, a, firstID)
	a.store.dailyLimit = 10
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 429, &rejection)
	if rejection.Error.Code != "DAILY_REQUEST_QUOTA_EXCEEDED" {
		t.Fatalf("wrong daily quota error: %s", rejection.Error.Code)
	}
	assertCounts(1, 1)
	a.store.dailyLimit = 30
	a.intelligence.interval = time.Millisecond
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs/"+firstID+"/rerun", nil, 202, &created)
	repeated := waitIntelligence(t, a, created.Run.RunID)
	if repeated.ExperimentID != experimentID || repeated.IntelligenceReport.Score != 100 {
		t.Fatal("rerun failed to reuse original experiment")
	}
	assertCounts(1, 2)
	request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
	if created.Run.ExperimentID == experimentID {
		t.Fatal("new request reused existing experiment")
	}
	waitIntelligence(t, a, created.Run.RunID)
	assertCounts(2, 3)
}
