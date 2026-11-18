package main

import (
	"net/http/httptest"
	"testing"
)

func TestInviteRegistrationAndIsolation(t *testing.T) {
	t.Setenv("INVITE_CODE", "test-invite-fixture")
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()
	a, b := clientWithCookies(), clientWithCookies()
	var experiment Experiment
	request(t, a, "POST", server.URL+"/api/v1/experiments", testConfig(), 201, &experiment)
	register := map[string]any{"username": "alice", "password": "test-password-fixture", "invite": "invalid-fixture", "claim_anonymous": true}
	request(t, a, "POST", server.URL+"/api/v1/auth/register", register, 403, nil)
	register["invite"] = "test-invite-fixture"
	register["password"] = "short"
	request(t, a, "POST", server.URL+"/api/v1/auth/register", register, 400, nil)
	register["password"] = "test-password-fixture"
	request(t, a, "POST", server.URL+"/api/v1/auth/register", register, 201, nil)
	request(t, a, "GET", server.URL+"/api/v1/experiments/"+experiment.ExperimentID, nil, 200, nil)
	request(t, b, "GET", server.URL+"/api/v1/experiments/"+experiment.ExperimentID, nil, 404, nil)
	request(t, b, "POST", server.URL+"/api/v1/auth/register", register, 409, nil)
	request(t, a, "POST", server.URL+"/api/v1/auth/logout", map[string]any{}, 200, nil)
	request(t, a, "GET", server.URL+"/api/v1/experiments/"+experiment.ExperimentID, nil, 404, nil)
	request(t, a, "POST", server.URL+"/api/v1/auth/login", map[string]string{"username": "alice", "password": "test-password-fixture"}, 200, nil)
	request(t, a, "GET", server.URL+"/api/v1/experiments/"+experiment.ExperimentID, nil, 200, nil)
}

func TestInviteSecureStartRequiresLogin(t *testing.T) {
	app, err := newApp(":memory:", []byte("test-session-secret-at-least-32-bytes"), true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewTLSServer(app)
	defer server.Close()
	client := server.Client()
	client.Jar = clientWithCookies().Jar
	var experiment Experiment
	request(t, client, "POST", server.URL+"/api/v1/experiments", testConfig(), 201, &experiment)
	request(t, client, "POST", server.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 401, nil)
}
