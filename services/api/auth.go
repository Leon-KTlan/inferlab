package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionLifetime = 7 * 24 * time.Hour

type identity struct {
	Owner     string
	UserID    string
	Username  string
	TokenHash string
	Legacy    bool
}
type identityKey struct{}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)
var errSessionChanged = errors.New("anonymous identity already claimed")
var errUsernameTaken = errors.New("username unavailable")
var errSessionCapacity = errors.New("session capacity reached")

func sessionCapacity(tx *sql.Tx, now int64, anonymous bool) error {
	if _, err := tx.Exec("DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions WHERE expires_at<=? OR revoked=1 LIMIT 128)", now); err != nil {
		return err
	}
	var total, visitors int
	if err := tx.QueryRow("SELECT count(*),COALESCE(sum(user_id IS NULL),0) FROM sessions").Scan(&total, &visitors); err != nil {
		return err
	}
	if total >= 8192 || anonymous && visitors >= 4096 {
		return errSessionCapacity
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *App) sessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "inferlab_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds()), Expires: a.store.now().Add(sessionLifetime).UTC()})
}

func (a *App) identify(w http.ResponseWriter, r *http.Request, create bool) (identity, error) {
	now := a.store.now().Unix()
	if cookie, err := r.Cookie("inferlab_session"); err == nil && len(cookie.Value) <= 160 {
		id := identity{TokenHash: hashToken(cookie.Value)}
		err := a.store.db.QueryRow(`SELECT s.owner,COALESCE(s.user_id,''),COALESCE(u.username,'') FROM sessions s LEFT JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND s.revoked=0`, id.TokenHash, now).Scan(&id.Owner, &id.UserID, &id.Username)
		if err == nil {
			return id, nil
		}
		if !isNotFound(err) {
			return identity{}, err
		}
		// Upgrade a legacy signed cookie once, preserving its anonymous records.
		parts := strings.Split(cookie.Value, ".")
		if len(parts) == 2 && len(parts[0]) == 40 && strings.HasPrefix(parts[0], "visitor_") && hmac.Equal([]byte(parts[1]), []byte(a.sign(parts[0]))) {
			tx, err := a.store.db.Begin()
			if err != nil {
				return identity{}, err
			}
			defer tx.Rollback()
			var count int
			err = tx.QueryRow("SELECT (SELECT count(*) FROM settings WHERE key=?) + (SELECT count(*) FROM anonymous_claims WHERE owner=?)", "legacy:"+parts[0], parts[0]).Scan(&count)
			if err != nil {
				return identity{}, err
			}
			if count == 0 {
				if !create {
					tx.Rollback()
					id.Owner = parts[0]
					id.Legacy = true
					return id, nil
				}
				if err = sessionCapacity(tx, now, true); err != nil {
					return identity{}, err
				}
				if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?)", "legacy:"+parts[0], []byte("migrated")); err != nil {
					return identity{}, err
				}
				if _, err = tx.Exec("INSERT INTO sessions(token_hash,owner,expires_at) VALUES(?,?,?)", id.TokenHash, parts[0], now+int64(sessionLifetime.Seconds())); err != nil {
					return identity{}, err
				}
				if err = tx.Commit(); err != nil {
					return identity{}, err
				}
				id.Owner = parts[0]
				a.sessionCookie(w, cookie.Value)
				return id, nil
			}
			tx.Rollback()
		}
	}
	if !create {
		return identity{}, nil
	}
	token := newID("session")
	id := identity{Owner: newID("visitor"), TokenHash: hashToken(token)}
	tx, err := a.store.db.Begin()
	if err != nil {
		return identity{}, err
	}
	defer tx.Rollback()
	if err = sessionCapacity(tx, now, true); err != nil {
		return identity{}, err
	}
	_, err = tx.Exec("INSERT INTO sessions(token_hash,owner,expires_at) VALUES(?,?,?)", id.TokenHash, id.Owner, now+int64(sessionLifetime.Seconds()))
	if err != nil {
		return identity{}, err
	}
	if err = tx.Commit(); err != nil {
		return identity{}, err
	}
	a.sessionCookie(w, token)
	return id, nil
}

func (a *App) identityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/api/v1/healthz" || strings.HasPrefix(r.URL.Path, "/api/v1/shares/") {
			next.ServeHTTP(w, r)
			return
		}
		// Authentication failures and stateless runner capabilities must not mint sessions.
		create := r.URL.Path != "/api/v1/auth/login" && r.URL.Path != "/api/v1/auth/register" && r.URL.Path != "/api/v1/auth/logout" && !strings.HasPrefix(r.URL.Path, "/api/v1/runner-tasks/")
		id, err := a.identify(w, r, create)
		if err != nil {
			writeError(w, 503, "SESSION_UNAVAILABLE", "could not load session")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}

func requestIdentity(r *http.Request) identity {
	id, _ := r.Context().Value(identityKey{}).(identity)
	return id
}

func (a *App) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if a.secure && requestIdentity(r).UserID == "" {
		writeError(w, 401, "LOGIN_REQUIRED", "sign in before starting an experiment")
		return false
	}
	return true
}

func sameOrigin(r *http.Request, secure bool) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil || secure {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
}

func requireAuthJSON(w http.ResponseWriter, r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "CONTENT_TYPE_REQUIRED", "use application/json")
		return false
	}
	return true
}

// Both dimensions are reserved before parsing credentials or running bcrypt.
func (a *App) authRate(w http.ResponseWriter, r *http.Request, username string) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	keys := []string{hashToken(r.URL.Path + ":ip:" + ip)}
	if username != "" {
		keys = append(keys, hashToken("login:user:"+username))
	}
	ok, err := a.store.authAttempt(keys)
	if err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "authentication temporarily unavailable")
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", "600")
		writeError(w, 429, "AUTH_RATE_LIMITED", "too many attempts; retry after ten minutes")
		return false
	}
	return true
}

func (s *Store) authAttempt(keys []string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	if _, err = tx.Exec("DELETE FROM login_attempts WHERE bucket IN (SELECT bucket FROM login_attempts WHERE window_start<=? LIMIT 512)", now-600); err != nil {
		return false, err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions WHERE expires_at<=? LIMIT 512)", now); err != nil {
		return false, err
	}
	var size int
	if err = tx.QueryRow("SELECT count(*) FROM login_attempts").Scan(&size); err != nil {
		return false, err
	}
	ok := true
	for _, key := range keys {
		var hits int
		err = tx.QueryRow("SELECT failures FROM login_attempts WHERE bucket=?", key).Scan(&hits)
		if isNotFound(err) {
			if size >= 4096 {
				ok = false
				continue
			}
			_, err = tx.Exec("INSERT INTO login_attempts(bucket,window_start,failures) VALUES(?,?,1)", key, now)
			size++
		} else if err == nil {
			if hits >= 5 {
				ok = false
			} else {
				_, err = tx.Exec("UPDATE login_attempts SET failures=failures+1 WHERE bucket=?", key)
			}
		}
		if err != nil {
			return false, err
		}
	}
	return ok, tx.Commit()
}

func (a *App) authMe(w http.ResponseWriter, r *http.Request) {
	id := requestIdentity(r)
	quota, err := a.store.quota(id.Owner)
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"username": id.Username, "authenticated": id.UserID != "", "login_required": a.secure, "registration_open": os.Getenv("INVITE_CODE") != "", "quota": quota})
}

type authInput struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Invite         string `json:"invite"`
	ClaimAnonymous bool   `json:"claim_anonymous"`
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	if !requireAuthJSON(w, r) || !a.authRate(w, r, "") {
		return
	}
	var input authInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 72 {
		writeError(w, 400, "INVALID_ACCOUNT", "username must be 3-32 ASCII letters, digits or underscores; password must be 12-72 bytes")
		return
	}
	expected := os.Getenv("INVITE_CODE")
	if expected == "" || !hmac.Equal([]byte(hashToken(input.Invite)), []byte(hashToken(expected))) {
		writeError(w, 403, "INVITE_INVALID", "registration requires a valid invitation")
		return
	}
	id := requestIdentity(r)
	if id.UserID != "" {
		writeError(w, 409, "ALREADY_SIGNED_IN", "sign out before registering")
		return
	}
	if id.Owner == "" {
		id.Owner = newID("visitor")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "AUTH_UNAVAILABLE", "could not create account")
		return
	}
	userID, token := newID("user"), newID("session")
	err = a.store.registerAccount(id, input, userID, hash, hashToken(token))
	if errors.Is(err, errUsernameTaken) {
		writeError(w, 409, "USERNAME_UNAVAILABLE", "username is unavailable")
		return
	}
	if errors.Is(err, errSessionChanged) {
		writeError(w, 409, "SESSION_CHANGED", "anonymous records were already claimed")
		return
	}
	if err != nil {
		writeError(w, 500, "AUTH_UNAVAILABLE", "could not create account")
		return
	}
	a.sessionCookie(w, token)
	writeJSON(w, 201, map[string]any{"username": input.Username, "authenticated": true, "anonymous_claimed": input.ClaimAnonymous})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !requireAuthJSON(w, r) || !a.authRate(w, r, "") {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 72 {
		writeError(w, 401, "LOGIN_INVALID", "invalid username or password")
		return
	}
	// IP was already charged; reserve the username bucket separately.
	ok, err := a.store.authAttempt([]string{hashToken("login:user:" + input.Username)})
	if err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "authentication temporarily unavailable")
		return
	}
	if !ok {
		writeError(w, 429, "AUTH_RATE_LIMITED", "too many attempts; retry after ten minutes")
		return
	}
	var userID string
	var hash []byte
	err = a.store.db.QueryRow("SELECT id,password_hash FROM users WHERE username=?", input.Username).Scan(&userID, &hash)
	if isNotFound(err) {
		hash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")
	} else if err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "authentication temporarily unavailable")
		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(input.Password)) != nil || userID == "" {
		writeError(w, 401, "LOGIN_INVALID", "invalid username or password")
		return
	}
	token := newID("session")
	if err = a.store.rotateSession(requestIdentity(r), userID, hashToken(token)); err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "could not create session")
		return
	}
	a.sessionCookie(w, token)
	writeJSON(w, 200, map[string]any{"username": input.Username, "authenticated": true, "anonymous_data_policy": "Anonymous records stay with the previous anonymous session and are not merged. Signing in revokes that session; export needed reports before signing in."})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if !requireAuthJSON(w, r) {
		return
	}
	var input struct{}
	if !decodeJSON(w, r, &input) {
		return
	}
	if _, err := a.store.db.Exec("UPDATE sessions SET revoked=1 WHERE token_hash=?", requestIdentity(r).TokenHash); err != nil {
		writeError(w, 503, "AUTH_UNAVAILABLE", "could not revoke session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "inferlab_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"signed_out": true})
}

func unclaimedOwner(tx *sql.Tx, owner string) error {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM anonymous_claims WHERE owner=?", owner).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errSessionChanged
	}
	return nil
}

func currentOwner(tx *sql.Tx, owner string) (string, error) {
	var claimed string
	err := tx.QueryRow("SELECT user_id FROM anonymous_claims WHERE owner=?", owner).Scan(&claimed)
	if isNotFound(err) {
		return owner, nil
	}
	return claimed, err
}

func (s *Store) registerAccount(id identity, input authInput, userID string, hash []byte, tokenHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM users WHERE username=?", input.Username).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errUsernameTaken
	}
	if err = unclaimedOwner(tx, id.Owner); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO users(id,username,password_hash) VALUES(?,?,?)", userID, input.Username, hash); err != nil {
		return err
	}
	if input.ClaimAnonymous {
		for _, query := range []string{"UPDATE experiments SET owner=? WHERE owner=?", "UPDATE runs SET owner=? WHERE owner=?", "UPDATE report_shares SET owner=? WHERE owner=?"} {
			if _, err = tx.Exec(query, userID, id.Owner); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO daily_quotas(owner,day,reserved) SELECT ?,day,reserved FROM daily_quotas WHERE owner=? ON CONFLICT(owner,day) DO UPDATE SET reserved=reserved+excluded.reserved", userID, id.Owner); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM daily_quotas WHERE owner=?", id.Owner); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO anonymous_claims(owner,user_id) VALUES(?,?)", id.Owner, userID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE sessions SET revoked=1 WHERE owner=?", id.Owner); err != nil {
			return err
		}
	}
	if err = s.newUserSession(tx, id, userID, tokenHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) newUserSession(tx *sql.Tx, old identity, userID, tokenHash string) error {
	if old.Legacy {
		if _, err := tx.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES(?,?)", "legacy:"+old.Owner, []byte("revoked")); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE sessions SET revoked=1 WHERE token_hash=?", old.TokenHash); err != nil {
		return err
	}
	if err := sessionCapacity(tx, s.now().Unix(), false); err != nil {
		return err
	}
	_, err := tx.Exec("INSERT INTO sessions(token_hash,owner,user_id,expires_at) VALUES(?,?,?,?)", tokenHash, userID, userID, s.now().Add(sessionLifetime).Unix())
	return err
}

func (s *Store) rotateSession(old identity, userID, tokenHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.newUserSession(tx, old, userID, tokenHash); err != nil {
		return err
	}
	return tx.Commit()
}
