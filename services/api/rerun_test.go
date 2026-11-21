package main

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRerunUsesOwnedSnapshotAndReservesQuota(t *testing.T) {
	app, server := inviteApp(t)
	app.store.dailyLimit = 10
	client := clientWithCookies()
	config := testConfig()
	config.Workload.MaxRequests = 5
	var experiment Experiment
	request(t, client, "POST", server.URL+"/api/v1/experiments", config, 201, &experiment)
	var source Run
	request(t, client, "POST", server.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 202, &source)
	waitRun(t, client, server.URL, source.RunID)

	experiment.Workload.MaxRequests = 6
	data, err := json.Marshal(experiment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.db.Exec("UPDATE experiments SET data=? WHERE id=?", data, experiment.ExperimentID); err != nil {
		t.Fatal(err)
	}

	path := server.URL + "/api/v1/runs/" + source.RunID + "/rerun"
	request(t, clientWithCookies(), "POST", path, nil, 404, nil)
	var replica Run
	request(t, client, "POST", path, nil, 202, &replica)
	if replica.RunID == source.RunID || replica.ExperimentID != source.ExperimentID || !reflect.DeepEqual(replica.Config, source.Config) {
		t.Fatalf("replica does not preserve the source snapshot: %+v", replica)
	}
	waitRun(t, client, server.URL, replica.RunID)
	var failure struct {
		Error struct{ Code string }
	}
	request(t, client, "POST", path, nil, 429, &failure)
	if failure.Error.Code != "DAILY_REQUEST_QUOTA_EXCEEDED" {
		t.Fatalf("rerun did not reserve quota: %+v", failure)
	}
}

func TestRerunInSecureModeRequiresLogin(t *testing.T) {
	app, err := newApp(":memory:", []byte("test-session-secret-at-least-32-bytes"), true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewTLSServer(app)
	defer server.Close()
	request(t, server.Client(), "POST", server.URL+"/api/v1/runs/run_absent/rerun", nil, 401, nil)
}
