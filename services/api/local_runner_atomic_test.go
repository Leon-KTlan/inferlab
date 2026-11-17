package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type gatedRunnerBody struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *gatedRunnerBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.Reader.Read(p)
}

func (b *gatedRunnerBody) Close() error { return nil }

func seedQueuedLocalRun(t *testing.T, app *App) (string, Run) {
	t.Helper()
	owner := newID("visitor")
	config := testConfig()
	config.Target = Target{Type: "openai_compatible", Endpoint: "https://203.0.113.1/v1", Model: "test-model"}
	experiment := Experiment{ExperimentID: newID("exp"), Config: config}
	if err := app.store.createExperiment(owner, experiment); err != nil {
		t.Fatal(err)
	}
	run := Run{RunID: newID("run"), ExperimentID: experiment.ExperimentID, Status: "queued", SourceType: "openai_compatible", Config: config, StageSummaries: []StageSummary{}}
	if err := app.store.createRun(owner, run); err != nil {
		t.Fatal(err)
	}
	return owner, run
}

func TestRunnerResultCannotOverwriteConcurrentTerminalState(t *testing.T) {
	for _, action := range []string{"cancel", "submit"} {
		t.Run(action, func(t *testing.T) {
			app, err := newApp(":memory:", nil, false, "")
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			owner, run := seedQueuedLocalRun(t, app)
			data, err := json.Marshal(validLocalResult(run.RunID, run.Config))
			if err != nil {
				t.Fatal(err)
			}
			body := &gatedRunnerBody{Reader: bytes.NewReader(data), entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(body.release) }) }
			defer release()
			path := "/api/v1/runner-tasks/" + run.RunID + "/result"
			request := httptest.NewRequest(http.MethodPost, path, body)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-InferLab-Runner-Token", app.runnerToken(run.RunID))
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); app.ServeHTTP(recorder, request) }()
			select {
			case <-body.entered:
			case <-done:
				t.Fatalf("POST returned before reading its body: %d", recorder.Code)
			case <-time.After(2 * time.Second):
				t.Fatal("POST did not reach the body gate")
			}
			winner := httptest.NewRecorder()
			if action == "cancel" {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/"+run.RunID+"/cancel", nil)
				req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + app.sign(owner)})
				app.ServeHTTP(winner, req)
			} else {
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-InferLab-Runner-Token", app.runnerToken(run.RunID))
				app.ServeHTTP(winner, req)
			}
			if winner.Code != http.StatusAccepted {
				t.Fatalf("winning %s returned %d", action, winner.Code)
			}
			release()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("blocked POST did not finish")
			}
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusConflict || failure.Error.Code != "RUN_ALREADY_FINISHED" {
				t.Fatalf("late POST returned %d %s", recorder.Code, failure.Error.Code)
			}
			persisted, err := app.store.run(owner, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "succeeded"
			wantEvent := "local_runner_succeeded"
			if action == "cancel" {
				wantStatus = "canceled"
				wantEvent = "run_canceled"
			}
			if persisted.Status != wantStatus {
				t.Fatalf("late POST changed terminal status to %s", persisted.Status)
			}
			events, err := app.store.events(run.RunID, 0)
			if err != nil || len(events) != 1 || events[0].Type != wantEvent {
				t.Fatalf("losing transition wrote an event: %+v err=%v", events, err)
			}
		})
	}
}

func TestLocalRunFinishCannotOverwriteAcceptedResult(t *testing.T) {
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	owner, stale := seedQueuedLocalRun(t, app)
	finished := stale
	finished.Status = "succeeded"
	if err := app.store.finishLocalRun(owner, finished, "local_runner_succeeded"); err != nil {
		t.Fatal(err)
	}
	stale.Status = "canceled"
	stale.ErrorCode = "RUN_CANCELED"
	if err := app.store.finishLocalRun(owner, stale, "run_canceled"); !errors.Is(err, errRunAlreadyFinished) {
		t.Fatalf("stale cancel was not rejected: %v", err)
	}
	actual, err := app.store.run(owner, stale.RunID)
	if err != nil || actual.Status != "succeeded" {
		t.Fatal("stale cancel overwrote the accepted result")
	}
}
