package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestRegistrationSetupSecureTokenAndPrivateStatus(t *testing.T) {
	for _, environment := range []string{"staging", "production", "unknown"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			t.Setenv("ADMIN_SETUP_TOKEN", "")
			a, err := newApp(":memory:", []byte("fixture-session-secret-at-least-32-bytes"), environment != "unknown", "")
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil), 403)
			t.Setenv("ADMIN_SETUP_TOKEN", "bootstrap-secret-sentinel")
			body := setupBody("admin")
			body["setup_token"] = "wrong-secret-sentinel"
			expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", body, nil), 403)
			w := localSettingsCall(a, "GET", "/api/v1/auth/me", nil, nil)
			if strings.Contains(w.Body.String(), `"setup_required":true`) {
				t.Fatal("public status exposed initialization")
			}
			body["setup_token"] = "bootstrap-secret-sentinel"
			w = localSettingsCall(a, "POST", "/api/v1/auth/setup", body, nil)
			expectSettingsStatus(t, w, 201)
			if environment != "unknown" && !w.Result().Cookies()[0].Secure {
				t.Fatal("insecure admin cookie")
			}
			if strings.Contains(w.Body.String(), body["setup_token"]) || strings.Contains(w.Body.String(), body["password"]) {
				t.Fatal("setup leaked credential")
			}
			expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", body, nil), 409)
		})
	}
}

func TestRegistrationSetupDefaultEnvironmentAndIPv6(t *testing.T) {
	a := settingsApp(t)
	t.Setenv("APP_ENV", "")
	w := settingsCall(a, "GET", "/api/v1/auth/me", nil, nil, "[::1]:9876", "[::1]:12345", nil)
	if !strings.Contains(w.Body.String(), `"setup_required":true`) {
		t.Fatal("default local initialization missing")
	}
	w = settingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil, "[::1]:9876", "[::1]:12345", map[string]string{"Origin": "http://[::1]:9876"})
	expectSettingsStatus(t, w, 201)
}

func TestRegistrationSetupValidationRateAndAtomicRetry(t *testing.T) {
	a := settingsApp(t)
	for _, password := range []string{"short", strings.Repeat("界", 25)} {
		body := setupBody("admin")
		body["password"] = password
		expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", body, nil), 400)
	}
	if _, err := a.store.db.Exec(`CREATE TRIGGER fail_setup BEFORE INSERT ON admin_audit BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil), 503)
	for _, table := range []string{"users", "sessions", "admin_audit"} {
		var count int
		if err := a.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial setup", table, err)
		}
	}
	if _, err := a.store.db.Exec("DROP TRIGGER fail_setup"); err != nil {
		t.Fatal(err)
	}
	body := setupBody("admin")
	body["password"] = strings.Repeat("界", 24)
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", body, nil), 201)
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("other"), nil), 409)
	w := localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("sixth"), nil)
	expectSettingsStatus(t, w, 429)
	if w.Header().Get("Retry-After") != "600" {
		t.Fatal("missing retry hint")
	}
}

func TestRegistrationSetupDuplicateDoesNotPromoteOrLoseRecords(t *testing.T) {
	a := settingsApp(t)
	t.Setenv("INVITE_CODE", "old")
	body := map[string]any{"username": "existing", "password": "visitor-password-fixture", "invite": "old"}
	w := localSettingsCall(a, "POST", "/api/v1/auth/register", body, nil)
	expectSettingsStatus(t, w, 201)
	cookie := w.Result().Cookies()[0]
	w = localSettingsCall(a, "GET", "/api/v1/auth/me", nil, cookie)
	if !strings.Contains(w.Body.String(), `"role":"user"`) {
		t.Fatal("first registered user was promoted")
	}
	var experiment Experiment
	w = localSettingsCall(a, "POST", "/api/v1/experiments", testConfig(), cookie)
	expectSettingsStatus(t, w, 201)
	if err := json.Unmarshal(w.Body.Bytes(), &experiment); err != nil {
		t.Fatal(err)
	}
	expectSettingsStatus(t, localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("existing"), nil), 409)
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count); err != nil || count != 0 {
		t.Fatal("duplicate created admin", err)
	}
	w = localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("independent"), nil)
	expectSettingsStatus(t, w, 201)
	expectSettingsStatus(t, localSettingsCall(a, "GET", "/api/v1/experiments/"+experiment.ExperimentID, nil, cookie), 200)
	expectSettingsStatus(t, localSettingsCall(a, "GET", "/api/v1/admin/registration", nil, cookie), 403)
}

func TestRegistrationSettingsFailedSaveAndFinalInviteValidation(t *testing.T) {
	a := settingsApp(t)
	w := localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil)
	expectSettingsStatus(t, w, 201)
	admin := w.Result().Cookies()[0]
	for _, invite := range []string{"1234567", strings.Repeat(" ", 8), strings.Repeat("\uFEFF", 3), "12345678\n", "12345678\u0085", strings.Repeat("x", 257)} {
		expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true, "invite": invite}, admin), 400)
	}
	old := "settings-old-sentinel"
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true, "invite": old}, admin), 200)
	if _, err := a.store.db.Exec(`CREATE TRIGGER fail_settings BEFORE INSERT ON admin_audit WHEN NEW.operation='registration_updated' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": false, "invite": "failed-save-sentinel"}, admin), 503)
	c, err := readRegistrationConfig(a.store.db)
	if err != nil || !c.accepts(old) {
		t.Fatal("failed save changed effective settings", err)
	}
	if _, err := a.store.db.Exec("DROP TRIGGER fail_settings"); err != nil {
		t.Fatal(err)
	}
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true, "invite": "replacement-sentinel"}, admin), 200)
	// A registration whose initial invitation check preceded the settings commit must recheck in its transaction.
	err = a.store.registerAccountTransaction(identity{Owner: "stale-owner"}, authInput{Username: "stale", Invite: old}, newID("user"), []byte("unused"), hashToken("stale"), true)
	if err != errInviteInvalid {
		t.Fatal("stale invite survived final check", err)
	}
	for _, cookie := range []*http.Cookie{admin, nil} {
		w = localSettingsCall(a, "GET", "/api/v1/auth/me", nil, cookie)
		if strings.Contains(w.Body.String(), old) || strings.Contains(w.Body.String(), hashToken(old)) {
			t.Fatal("identity leaked invite")
		}
	}
	w = localSettingsCall(a, "GET", "/api/v1/admin/registration", nil, admin)
	for _, secret := range []string{old, hashToken(old), "replacement-sentinel", hashToken("replacement-sentinel")} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("settings leaked invite")
		}
	}
	var audits int
	if err = a.store.db.QueryRow("SELECT count(*) FROM admin_audit").Scan(&audits); err != nil || audits != 3 {
		t.Fatal("audit atomicity failed", err)
	}
}

func TestRegistrationSettingsPersistenceAndEnvironmentPriority(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("INVITE_CODE", "old")
	path := filepath.Join(t.TempDir(), "settings.db")
	a, err := newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	w := localSettingsCall(a, "POST", "/api/v1/auth/setup", setupBody("admin"), nil)
	if w.Code != 201 {
		a.Close()
		t.Fatalf("setup status %d", w.Code)
	}
	admin := w.Result().Cookies()[0]
	// Legacy short codes remain valid when merely closing/reopening registration.
	for _, enabled := range []bool{false, true, false} {
		w = localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": enabled}, admin)
		if w.Code != 200 {
			a.Close()
			t.Fatalf("legacy toggle status %d", w.Code)
		}
	}
	a.Close()
	t.Setenv("INVITE_CODE", "new-environment-sentinel")
	a, err = newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := readRegistrationConfig(a.store.db)
	if err != nil || c.Enabled || c.Source != "database" || !c.InviteConfigured {
		t.Fatal("database priority lost", err)
	}
	expectSettingsStatus(t, localSettingsCall(a, "GET", "/api/v1/admin/registration", nil, admin), 200)
	expectSettingsStatus(t, localSettingsCall(a, "PUT", "/api/v1/admin/registration", map[string]any{"enabled": true}, admin), 200)
	c, err = readRegistrationConfig(a.store.db)
	if err != nil || !c.accepts("old") || c.accepts("new-environment-sentinel") {
		t.Fatal("legacy code not preserved", err)
	}
}

func TestRegistrationV2MigrationPreservesUsersSessionsAndRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		if err = applySchemaMigration(tx, version); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("existing-password-fixture"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO users VALUES('existing-id','existing',?);
INSERT INTO sessions VALUES('session-hash','existing-id','existing-id',?,0);
INSERT INTO experiments VALUES('exp','existing-id','{}');
INSERT INTO runs VALUES('run','exp','existing-id','succeeded','{}');
INSERT INTO daily_quotas VALUES('existing-id','2026-11-18',42);
INSERT INTO report_shares VALUES('share','run','existing-id',1);
PRAGMA user_version=2;`, hash, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var role string
	var actualHash []byte
	if err = s.db.QueryRow("SELECT role,password_hash FROM users WHERE id='existing-id'").Scan(&role, &actualHash); err != nil || role != "user" || string(actualHash) != string(hash) {
		t.Fatal("user migration changed account", err)
	}
	for _, query := range []string{
		"SELECT count(*) FROM sessions WHERE token_hash='session-hash' AND revoked=0",
		"SELECT count(*) FROM experiments WHERE id='exp' AND data='{}'",
		"SELECT count(*) FROM runs WHERE id='run' AND data='{}'",
		"SELECT count(*) FROM daily_quotas WHERE reserved=42",
		"SELECT count(*) FROM report_shares WHERE token='share'",
	} {
		var count int
		if err = s.db.QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatal("migration lost record", err)
		}
	}
	if err = s.migrate(); err != nil {
		t.Fatal("migration not repeatable", err)
	}
}
