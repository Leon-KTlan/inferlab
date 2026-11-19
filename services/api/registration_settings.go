package main

import (
	"crypto/hmac"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var errInviteInvalid = errors.New("invalid invitation")
var errSetupComplete = errors.New("site already initialized")
var errInviteRequired = errors.New("valid invitation required to enable registration")

type registrationConfig struct {
	Enabled          bool   `json:"enabled"`
	InviteConfigured bool   `json:"invite_configured"`
	Source           string `json:"source"`
	inviteHash       string
}

type registrationReader interface {
	QueryRow(string, ...any) *sql.Row
}

func readRegistrationConfig(db registrationReader) (registrationConfig, error) {
	c := registrationConfig{Source: "database"}
	err := db.QueryRow("SELECT enabled,invite_hash FROM registration_settings WHERE id=1").Scan(&c.Enabled, &c.inviteHash)
	if errors.Is(err, sql.ErrNoRows) {
		c.Source = "environment"
		if invite := os.Getenv("INVITE_CODE"); invite != "" {
			c.Enabled = true
			c.inviteHash = hashToken(invite)
		}
		err = nil
	}
	c.InviteConfigured = c.inviteHash != ""
	return c, err
}

func (c registrationConfig) accepts(invite string) bool {
	return c.Enabled && c.InviteConfigured && hmac.Equal([]byte(c.inviteHash), []byte(hashToken(invite)))
}

func validNewInvite(invite string) bool {
	if len(invite) < 8 || len(invite) > 256 || !utf8.ValidString(invite) || strings.TrimFunc(invite, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }) == "" {
		return false
	}
	for _, c := range invite {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func (a *App) localSetupAllowed(r *http.Request) bool {
	if a.secure || env("APP_ENV", "local") != "local" {
		return false
	}
	for name := range r.Header {
		if strings.EqualFold(name, "Forwarded") || strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			return false
		}
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	u, err := url.Parse("http://" + r.Host)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host != r.Host {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func (a *App) setupAdmin(w http.ResponseWriter, r *http.Request) {
	if !requireAuthJSON(w, r) || !a.authRate(w, r, "") {
		return
	}
	var input struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		SetupToken string `json:"setup_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	expected := os.Getenv("ADMIN_SETUP_TOKEN")
	if !a.localSetupAllowed(r) && (expected == "" || !hmac.Equal([]byte(hashToken(input.SetupToken)), []byte(hashToken(expected)))) {
		writeError(w, 403, "SETUP_FORBIDDEN", "site initialization is not authorized")
		return
	}
	if requestIdentity(r).UserID != "" {
		writeError(w, 409, "ALREADY_SIGNED_IN", "sign out before initializing")
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 72 {
		writeError(w, 400, "INVALID_ACCOUNT", "invalid username or password length")
		return
	}
	// Reject completed initialization before spending work on bcrypt; recheck inside the transaction.
	var count int
	if err := a.store.db.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "initialization temporarily unavailable")
		return
	}
	if count != 0 {
		writeError(w, 409, "SETUP_COMPLETE", "site already initialized")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "AUTH_UNAVAILABLE", "could not initialize site")
		return
	}
	token := newID("session")
	err = a.store.createAdmin(requestIdentity(r), input.Username, hash, hashToken(token))
	if errors.Is(err, errUsernameTaken) {
		writeError(w, 409, "USERNAME_UNAVAILABLE", "username unavailable")
		return
	}
	if errors.Is(err, errSetupComplete) {
		writeError(w, 409, "SETUP_COMPLETE", "site already initialized")
		return
	}
	if err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "could not initialize site")
		return
	}
	a.sessionCookie(w, token)
	writeJSON(w, 201, map[string]any{"authenticated": true, "username": input.Username, "role": "admin"})
}

func (s *Store) createAdmin(old identity, username string, hash []byte, tokenHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errSetupComplete
	}
	if err = tx.QueryRow("SELECT count(*) FROM users WHERE username=?", username).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errUsernameTaken
	}
	userID := newID("user")
	if _, err = tx.Exec("INSERT INTO users(id,username,password_hash,role) VALUES(?,?,?,'admin')", userID, username, hash); err != nil {
		return err
	}
	if err = s.newUserSession(tx, old, userID, tokenHash); err != nil {
		return err
	}
	c, err := readRegistrationConfig(tx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO admin_audit(operation,actor_id,at,enabled) VALUES('site_initialized',?,?,?)", userID, s.now().Unix(), c.Enabled); err != nil {
		return err
	}
	return tx.Commit()
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if requestIdentity(r).Role != "admin" {
		writeError(w, 403, "ADMIN_REQUIRED", "administrator required")
		return false
	}
	return true
}

func (a *App) getRegistrationSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	c, err := readRegistrationConfig(a.store.db)
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (a *App) putRegistrationSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !requireAuthJSON(w, r) {
		return
	}
	var input struct {
		Enabled *bool  `json:"enabled"`
		Invite  string `json:"invite"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil || input.Invite != "" && !validNewInvite(input.Invite) {
		writeError(w, 400, "INVALID_REGISTRATION_SETTINGS", "provide enabled and an invitation of 8-256 UTF-8 bytes without control characters")
		return
	}
	c, err := a.store.saveRegistrationSettings(requestIdentity(r).UserID, *input.Enabled, input.Invite)
	if errors.Is(err, errInviteRequired) {
		writeError(w, 400, "INVITE_REQUIRED", "configure an invitation before enabling registration")
		return
	}
	if err != nil {
		writeError(w, 503, "SETTINGS_UNAVAILABLE", "could not save settings")
		return
	}
	writeJSON(w, 200, c)
}

func (s *Store) saveRegistrationSettings(actor string, enabled bool, invite string) (registrationConfig, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return registrationConfig{}, err
	}
	defer tx.Rollback()
	c, err := readRegistrationConfig(tx)
	if err != nil {
		return c, err
	}
	if invite != "" {
		c.inviteHash = hashToken(invite)
	}
	if enabled && c.inviteHash == "" {
		return c, errInviteRequired
	}
	if _, err = tx.Exec("INSERT INTO registration_settings(id,enabled,invite_hash) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,invite_hash=excluded.invite_hash", enabled, c.inviteHash); err != nil {
		return c, err
	}
	if _, err = tx.Exec("INSERT INTO admin_audit(operation,actor_id,at,enabled) VALUES('registration_updated',?,?,?)", actor, s.now().Unix(), enabled); err != nil {
		return c, err
	}
	c.Enabled, c.InviteConfigured, c.Source = enabled, c.inviteHash != "", "database"
	return c, tx.Commit()
}
