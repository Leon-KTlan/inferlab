package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func settingsApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("ADMIN_SETUP_TOKEN", "")
	t.Setenv("INVITE_CODE", "")
	a, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func settingsCall(a *App, method, path string, body any, cookie *http.Cookie, host, remote string, headers map[string]string) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Host = host
	r.RemoteAddr = remote
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func localSettingsCall(a *App, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	return settingsCall(a, method, path, body, cookie, "localhost:9876", "127.0.0.1:12345", nil)
}

func setupBody(username string) map[string]string {
	return map[string]string{"username": username, "password": "setup-password-test-fixture"}
}

func expectSettingsStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d", w.Code, status)
	}
}

func TestRegistrationSetupBoundary(t *testing.T) {
	a := settingsApp(t)
	for _, tc := range []struct {
		name, host, remote string
		headers            map[string]string
	}{
		{"public", "public.example", "203.0.113.8:9", nil},
		{"rebind", "attacker.example:9876", "127.0.0.1:9", nil},
		{"suffix", "localhost.attacker.example", "127.0.0.1:9", nil},
		{"spoofed-ip", "localhost:9876", "203.0.113.8:9", map[string]string{"X-Forwarded-For": "127.0.0.1"}},
		{"proxy", "localhost:9876", "127.0.0.1:9", map[string]string{"Forwarded": "for=127.0.0.1"}},
		{"origin", "localhost:9876", "127.0.0.1:9", map[string]string{"Origin": "http://evil.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := settingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil, tc.host, tc.remote, tc.headers)
			expectSettingsStatus(t, w, 403)
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("denial minted session")
			}
		})
	}
	w := localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil)
	expectSettingsStatus(t, w, 201)
	cookie := w.Result().Cookies()[0]
	w = localSettingsCall(a, "GET", "/api/v1/auth/me", nil, cookie)
	if !strings.Contains(w.Body.String(), `"role":"admin"`) {
		t.Fatal("setup did not sign in admin")
	}
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("another"), nil), 409)
}

func TestRegistrationSettingsPermissionsAndInvite(t *testing.T) {
	a := settingsApp(t)
	for _, method := range []string{"GET", "PUT"} {
		expectSettingsStatus(t, localSettingsCall(a, method, "/api/v1/admin/registration", map[string]any{"enabled": true}, nil), 403)
	}
	w := localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil)
	expectSettingsStatus(t, w, 201)
	admin := w.Result().Cookies()[0]
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true}, admin), 400)
	const invite = "settings-invite-sentinel"
	w = localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true, "invite": invite}, admin)
	expectSettingsStatus(t, w, 200)
	if strings.Contains(w.Body.String(), invite) {
		t.Fatal("settings echoed invite")
	}
	visitor := map[string]any{"username": "visitor", "password": "visitor-password-fixture", "invite": invite}
	w = localSettingsCall(a, "POST", "/api/v1/auth/register", visitor, nil)
	expectSettingsStatus(t, w, 201)
	ordinary := w.Result().Cookies()[0]
	for _, method := range []string{"GET", "PUT"} {
		expectSettingsStatus(t, localSettingsCall(a, method, "/api/v1/admin/registration", map[string]any{"enabled": false}, ordinary), 403)
	}
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true, "invite": "replacement-invite"}, admin), 200)
	visitor["username"] = "other"
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/register", visitor, nil), 403)
	visitor["invite"] = "replacement-invite"
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/register", visitor, nil), 201)
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": false}, admin), 200)
	visitor["username"] = "closed"
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/register", visitor, nil), 403)
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true}, admin), 200)
	visitor["username"] = "reopened"
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/register", visitor, nil), 201)
}

func TestRegistrationSetupConcurrentOnce(t *testing.T) {
	a := settingsApp(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 4)
	for _, username := range []string{"admin_a", "admin_b", "admin_c", "admin_d"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			statuses <- localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody(name), nil).Code
		}(username)
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == 201 {
			created++
		} else if status != 409 {
			t.Fatalf("unexpected concurrent status %d", status)
		}
	}
	if created != 1 {
		t.Fatalf("created %d administrators", created)
	}
}
