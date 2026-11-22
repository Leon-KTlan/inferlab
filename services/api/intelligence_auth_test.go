package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIntelligenceResultDoesNotCreateSessions(t *testing.T) {
	for _, saturated := range []bool{false, true} {
		name := "available"
		if saturated {
			name = "saturated"
		}
		t.Run(name, func(t *testing.T) {
			app, server, client := intelligenceTestApp(t)
			input := intelligenceInput("")
			input["target"] = Target{Type: "openai_compatible", Model: "fixture", Endpoint: "https://8.8.8.8/v1"}
			input["authorized"] = true
			var created struct {
				Run  Run        `json:"run"`
				Task RunnerTask `json:"task"`
			}
			request(t, client, "POST", server.URL+"/api/v1/intelligence/runs", input, 202, &created)
			if saturated {
				_, err := app.store.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<4095)
INSERT INTO sessions(token_hash,owner,expires_at) SELECT printf('intelligence-fixture-%d',x),printf('owner-%d',x),? FROM n`, time.Now().Add(time.Hour).Unix())
				if err != nil {
					t.Fatal(err)
				}
			}
			var before int
			if err := app.store.db.QueryRow("SELECT count(*) FROM sessions").Scan(&before); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(intelligenceValidResult(created.Run.Config, created.Run.RunID))
			if err != nil {
				t.Fatal(err)
			}
			for _, attempt := range []struct {
				token  string
				status int
			}{{"", 403}, {created.Task.TaskToken, 202}} {
				req := httptest.NewRequest("POST", created.Task.ResultEndpoint, bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-InferLab-Runner-Token", attempt.token)
				response := httptest.NewRecorder()
				app.ServeHTTP(response, req)
				if response.Code != attempt.status || len(response.Result().Cookies()) != 0 {
					t.Fatalf("stateless result returned status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
				}
			}
			var after int
			if err := app.store.db.QueryRow("SELECT count(*) FROM sessions").Scan(&after); err != nil || before != after {
				t.Fatalf("result requests changed session count: %d -> %d (%v)", before, after, err)
			}
		})
	}
}
