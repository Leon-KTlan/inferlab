package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func persistedComparisonRun(t *testing.T, store *Store, owner string, offset float64) Run {
	t.Helper()
	config := testConfig()
	experiment := Experiment{ExperimentID: newID("exp"), Config: config}
	if err := store.createExperiment(owner, experiment); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-2 * time.Second)
	finish := start.Add(2 * time.Second)
	run := Run{RunID: newID("run"), ExperimentID: experiment.ExperimentID, Status: "succeeded", SourceType: "mock", ResultKind: "demo", SoftwareVersion: "0.1.0", Config: config, StartedAt: &start, FinishedAt: &finish}
	var all []Sample
	for index, concurrency := range config.Workload.ConcurrencySteps {
		var samples []Sample
		// Unequal stage sample counts make the overall P95 differ from averaged stage P95.
		for n := 0; n < 20*(index+1); n++ {
			sample := Sample{Stage: index + 1, StartedAt: start, LatencyMS: float64(10*index+n+1) + offset}
			if n == 0 {
				sample.ErrorCode = "HTTP_503"
			}
			samples = append(samples, sample)
		}
		m := metrics(samples, len(samples), time.Second)
		run.StageSummaries = append(run.StageSummaries, StageSummary{Stage: index + 1, Concurrency: concurrency, Metrics: m, SLOStatus: sloStatus(m, config.SLO)})
		all = append(all, samples...)
	}
	run.Metrics = metrics(all, len(all), 2*time.Second)
	if err := store.createRun(owner, run); err != nil {
		t.Fatal(err)
	}
	for index := range all {
		if err := store.saveRun(run, &all[index], ""); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func TestComparePersistedRuns(t *testing.T) {
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner := newID("visitor")
	baseline := persistedComparisonRun(t, app.store, owner, 0)
	compare := persistedComparisonRun(t, app.store, owner, 10)
	// Changing a template after its runs finish must not change the compared snapshots.
	experiment, err := app.store.experiment(owner, compare.ExperimentID)
	if err != nil {
		t.Fatal(err)
	}
	experiment.Workload.MaxRPS = 1
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.db.Exec("UPDATE experiments SET data=? WHERE id=?", data, experiment.ExperimentID); err != nil {
		t.Fatal(err)
	}
	loadedBaseline, err := app.store.run(owner, baseline.RunID)
	if err != nil {
		t.Fatal(err)
	}
	loadedCompare, err := app.store.run(owner, compare.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if code, message := comparisonIssue(loadedBaseline, loadedCompare); code != "" {
		t.Fatalf("persisted snapshots became incomparable: %s %s", code, message)
	}
	if _, err := app.store.run("another-owner", baseline.RunID); !isNotFound(err) {
		t.Fatal("storage lookup exposed a foreign run")
	}
	server := httptest.NewServer(app)
	defer server.Close()
	client := &http.Client{}
	url := server.URL + "/api/v1/reports/" + compare.RunID + "/compare?baseline_run_id=" + baseline.RunID
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + app.sign(owner)})
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("comparison returned %d", response.StatusCode)
	}
	var result Comparison
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Comparable || result.BaselineRunID != baseline.RunID || result.CompareRunID != compare.RunID || len(result.Stages) != 2 || len(result.Notes) == 0 {
		t.Fatalf("incomplete comparison: %+v", result)
	}
	for _, stage := range result.Stages {
		if stage.Delta.P95E2EMS != 10 || stage.Delta.ErrorRate != 0 || stage.Delta.ThroughputRPS != 0 {
			t.Fatalf("wrong stage delta: %+v", stage)
		}
	}
	if result.Overall.Baseline.P95E2EMS != baseline.P95E2EMS || result.Overall.Delta.P95E2EMS != 10 || result.Overall.Baseline.P95E2EMS == (baseline.StageSummaries[0].P95E2EMS+baseline.StageSummaries[1].P95E2EMS)/2 {
		t.Fatalf("overall percentile was averaged or altered: %+v", result.Overall)
	}
	request(t, clientWithCookies(), "GET", url, nil, 404, nil)
}

func TestComparisonRejectsInvalidRuns(t *testing.T) {
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner := newID("visitor")
	baseline := persistedComparisonRun(t, app.store, owner, 0)
	compare := persistedComparisonRun(t, app.store, owner, 0)
	server := httptest.NewServer(app)
	defer server.Close()
	cases := []struct {
		name   string
		mutate func(*Run)
		code   string
	}{
		{"different rps", func(r *Run) { r.Config.Workload.MaxRPS++ }, "CONFIG_MISMATCH"},
		{"different stages", func(r *Run) { r.Config.Workload.ConcurrencySteps = []int{1, 4} }, "CONFIG_MISMATCH"},
		{"different target", func(r *Run) { r.Config.Target.Model = "other-model" }, "CONFIG_MISMATCH"},
		{"different slo", func(r *Run) { r.Config.SLO.P95E2EMS++ }, "CONFIG_MISMATCH"},
		{"different version", func(r *Run) { r.SoftwareVersion = "other-version" }, "CONFIG_MISMATCH"},
		{"running", func(r *Run) { r.Status = "running" }, "RUN_NOT_FINISHED"},
		{"canceled", func(r *Run) { r.Status = "canceled" }, "RUN_NOT_SUCCEEDED"},
		{"failed", func(r *Run) { r.Status = "failed" }, "RUN_NOT_SUCCEEDED"},
		{"low sample", func(r *Run) { r.StageSummaries[0].CompletedCount = 19; r.StageSummaries[0].RequestCount = 19 }, "INSUFFICIENT_SAMPLES"},
		{"missing stage", func(r *Run) { r.StageSummaries = r.StageSummaries[:1] }, "INCOMPLETE_STAGES"},
		{"stage mismatch", func(r *Run) { r.StageSummaries[1].Concurrency++ }, "INCOMPLETE_STAGES"},
		{"incomplete requests", func(r *Run) { r.StageSummaries[0].RequestCount++ }, "INCOMPLETE_STAGES"},
		{"short stage", func(r *Run) { r.StageSummaries[1].DurationSeconds = .5 }, "INCOMPLETE_STAGES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(compare)
			if err != nil {
				t.Fatal(err)
			}
			var changed Run
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			if err := app.store.saveRun(changed, nil, ""); err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("GET", server.URL+"/api/v1/reports/"+compare.RunID+"/compare?baseline_run_id="+baseline.RunID, nil)
			req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + app.sign(owner)})
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var result struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 409 || result.Error.Code != tc.code {
				t.Fatalf("got %d %s, want 409 %s", response.StatusCode, result.Error.Code, tc.code)
			}
		})
	}
}

func TestComparisonInputAndOwnership(t *testing.T) {
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner := newID("visitor")
	baseline := persistedComparisonRun(t, app.store, owner, 0)
	compare := persistedComparisonRun(t, app.store, owner, 0)
	foreign := persistedComparisonRun(t, app.store, newID("visitor"), 0)
	server := httptest.NewServer(app)
	defer server.Close()
	cases := []struct {
		path   string
		status int
		code   string
	}{
		{"/reports/" + compare.RunID + "/compare", 400, "INVALID_COMPARISON"},
		{"/reports/" + compare.RunID + "/compare?baseline_run_id=", 400, "INVALID_COMPARISON"},
		{"/reports/" + compare.RunID + "/compare?baseline_run_id=" + baseline.RunID + "&baseline_run_id=" + baseline.RunID, 400, "INVALID_COMPARISON"},
		{"/reports/" + compare.RunID + "/compare?baseline_run_id=" + compare.RunID, 400, "INVALID_COMPARISON"},
		{"/reports/" + foreign.RunID + "/compare?baseline_run_id=" + baseline.RunID, 404, "NOT_FOUND"},
		{"/reports/" + compare.RunID + "/compare?baseline_run_id=" + foreign.RunID, 404, "NOT_FOUND"},
		{"/reports/" + compare.RunID + "/compare?baseline_run_id=missing", 404, "NOT_FOUND"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest("GET", server.URL+"/api/v1"+tc.path, nil)
		req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + app.sign(owner)})
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.status || result.Error.Code != tc.code {
			t.Fatalf("%s: got %d %s, want %d %s", tc.path, response.StatusCode, result.Error.Code, tc.status, tc.code)
		}
	}
}
