package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const watchaEndpoint = "https://pay.watcha.cn/v1/entitlements/access"

type watchaAccess struct {
	key, entitlement string
	client           *http.Client
	mu               sync.Mutex
	window           time.Time
	sent             int
	slots            chan struct{}
}

type exportAccess struct {
	Access      string `json:"access"`
	PurchaseURL string `json:"purchase_url,omitempty"`
}

func configuredWatcha() (*watchaAccess, error) {
	if os.Getenv("PAYMENTS_ENABLED") != "true" {
		return nil, nil
	}
	var prefix string
	switch os.Getenv("APP_ENV") {
	case "staging":
		prefix = "WATCHA_SANDBOX_"
	case "production":
		prefix = "WATCHA_LIVE_"
	default:
		return nil, errors.New("real payments require staging or production")
	}
	key, entitlement := os.Getenv(prefix+"API_KEY"), os.Getenv(prefix+"ENTITLEMENT_ID")
	if strings.TrimSpace(key) == "" || strings.TrimSpace(entitlement) == "" {
		return nil, errors.New("payments require the runtime Key and entitlement ID for this environment")
	}
	return newWatcha(key, entitlement, nil, 3*time.Second), nil
}

func newWatcha(key, entitlement string, transport http.RoundTripper, timeout time.Duration) *watchaAccess {
	return &watchaAccess{key: key, entitlement: entitlement, slots: make(chan struct{}, 4), client: &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *watchaAccess) allowRequest(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Sub(p.window) >= time.Minute {
		p.window, p.sent = now, 0
	}
	if p.sent >= 60 {
		return false
	}
	p.sent++
	return true
}

func safePurchaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.User != nil || u.Opaque != "" || u.Fragment != "" || strings.ContainsAny(raw, "\\ \t\r\n") {
		return ""
	}
	switch u.Scheme {
	case "https":
		if u.Hostname() != "" && u.Port() == "" {
			return u.String()
		}
	case "alipays":
		if u.Host == "platformapi" && u.Path == "/startapp" && u.RawQuery != "" {
			return u.String()
		}
	}
	return ""
}

func (p *watchaAccess) check(ctx context.Context, userID string) (exportAccess, error) {
	body, _ := json.Marshal(map[string]string{"entitlement_id": p.entitlement, "user_id": userID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, watchaEndpoint, bytes.NewReader(body))
	if err != nil {
		return exportAccess{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.key)
	res, err := p.client.Do(req)
	if err != nil {
		return exportAccess{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return exportAccess{}, errors.New("unexpected payment HTTP status")
	}
	const maxResponse = 16 << 10
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return exportAccess{}, errors.New("invalid payment response size")
	}
	var payload struct {
		Access      string
		Entitlement struct{ Type string }
		Purchase    struct{ URL string }
	}
	if json.Unmarshal(data, &payload) != nil || payload.Entitlement.Type != "permanent" {
		return exportAccess{}, errors.New("unexpected payment response")
	}
	switch payload.Access {
	case "granted":
		return exportAccess{Access: "granted"}, nil
	case "purchase_required":
		return exportAccess{Access: payload.Access, PurchaseURL: safePurchaseURL(payload.Purchase.URL)}, nil
	default:
		return exportAccess{}, errors.New("entitlement unavailable")
	}
}

func (a *App) ownedExport(w http.ResponseWriter, r *http.Request) (Run, exportAccess, bool) {
	id := requestIdentity(r)
	if id.UserID == "" {
		writeError(w, 401, "LOGIN_REQUIRED", "sign in to export reports")
		return Run{}, exportAccess{}, false
	}
	if a.payments == nil {
		writeError(w, 404, "EXPORT_DISABLED", "report CSV export is disabled")
		return Run{}, exportAccess{}, false
	}
	run, err := a.store.run(id.Owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return Run{}, exportAccess{}, false
	}
	if !run.terminal() {
		writeError(w, 409, "RUN_NOT_FINISHED", "report is available when the run finishes")
		return Run{}, exportAccess{}, false
	}
	if r.Method != http.MethodGet {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "export requires GET")
		return Run{}, exportAccess{}, false
	}
	select {
	case a.payments.slots <- struct{}{}:
		defer func() { <-a.payments.slots }()
	default:
		writeError(w, 429, "EXPORT_BUSY", "try again later")
		return Run{}, exportAccess{}, false
	}
	if !a.payments.allowRequest(time.Now()) {
		writeError(w, 429, "EXPORT_BUSY", "try again later")
		return Run{}, exportAccess{}, false
	}
	result, err := a.payments.check(r.Context(), id.UserID)
	if err != nil {
		a.ops.paymentFailures.Add(1)
		writeError(w, 503, "EXPORT_UNAVAILABLE", "could not verify export access")
		return Run{}, exportAccess{}, false
	}
	return run, result, true
}

func (a *App) reportExportAccess(w http.ResponseWriter, r *http.Request) {
	if _, access, ok := a.ownedExport(w, r); ok {
		writeJSON(w, 200, access)
	}
}

func (a *App) reportCSV(w http.ResponseWriter, r *http.Request) {
	run, access, ok := a.ownedExport(w, r)
	if !ok {
		return
	}
	if access.Access != "granted" {
		writeError(w, 402, "PURCHASE_REQUIRED", "purchase is required to export this report")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="inferlab-report.csv"`)
	c := csv.NewWriter(w)
	c.Write([]string{"scope", "stage", "concurrency", "sent", "completed", "errors", "p50_ms", "p95_ms", "error_rate", "requests_per_second"})
	row := func(scope, stage, concurrency string, m Metrics) {
		decimal := func(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
		c.Write([]string{scope, stage, concurrency, strconv.Itoa(m.RequestCount), strconv.Itoa(m.CompletedCount), strconv.Itoa(m.ErrorCount), decimal(m.P50E2EMS), decimal(m.P95E2EMS), decimal(m.ErrorRate), decimal(m.ThroughputRPS)})
	}
	// Only fixed labels and numeric fields are exported; user text never becomes a cell.
	row("overall", "", "", run.Metrics)
	for _, s := range run.StageSummaries {
		row("stage", strconv.Itoa(s.Stage), strconv.Itoa(s.Concurrency), s.Metrics)
	}
	c.Flush()
}
