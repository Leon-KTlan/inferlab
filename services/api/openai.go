package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ErrEndpointInvalid  = "TARGET_ENDPOINT_INVALID"
	ErrSSRFBlocked      = "TARGET_SSRF_BLOCKED"
	ErrAuthFailed       = "TARGET_AUTH_FAILED"
	ErrRateLimited      = "TARGET_RATE_LIMITED"
	ErrHTTP4xx          = "TARGET_HTTP_4XX"
	ErrHTTP5xx          = "TARGET_HTTP_5XX"
	ErrRedirectBlocked  = "TARGET_REDIRECT_BLOCKED"
	ErrTargetTimeout    = "TARGET_TIMEOUT"
	ErrTargetCanceled   = "TARGET_CANCELED"
	ErrConnectionFailed = "TARGET_CONNECTION_FAILED"
	ErrResponseInvalid  = "TARGET_RESPONSE_INVALID"
	ErrResponseRead     = "TARGET_RESPONSE_READ_FAILED"
)

// EndpointPolicy is intentionally strict by default. AllowLoopback and TLSConfig
// exist only for tests and explicitly managed local development targets.
type EndpointPolicy struct {
	AllowLoopback bool
	TLSConfig     *tls.Config
	HTTPClient    *http.Client
	Resolve       func(context.Context, string, string) ([]net.IP, error)
}

func validateEndpoint(ctx context.Context, raw string, policy EndpointPolicy) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("%s: endpoint must be an HTTPS URL without credentials", ErrEndpointInvalid)
	}
	if u.Fragment != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("%s: endpoint must not contain a query or fragment", ErrEndpointInvalid)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%s: endpoint hostname is required", ErrEndpointInvalid)
	}
	if port := u.Port(); port != "" {
		parsed, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsed < 1 || parsed > 65535 {
			return nil, fmt.Errorf("%s: endpoint port is invalid", ErrEndpointInvalid)
		}
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = resolveEndpointIPs(ctx, host, policy)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("%s: endpoint DNS lookup failed", ErrEndpointInvalid)
		}
	}
	for _, ip := range ips {
		if blockedIP(ip, policy.AllowLoopback) {
			return nil, fmt.Errorf("%s: endpoint resolves to a restricted address", ErrSSRFBlocked)
		}
	}
	return u, nil
}

func blockedIP(ip net.IP, allowLoopback bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 0 || v4[0] >= 224 || v4[0] == 198 && v4[1]&0xfe == 18 || v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return true
		}
	} else {
		for _, prefix := range []string{"64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32"} {
			_, network, _ := net.ParseCIDR(prefix)
			if network.Contains(ip) {
				return true
			}
		}
	}
	// Shared address space includes Alibaba Cloud's 100.100.100.200 metadata service.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	if ip.IsLoopback() && !allowLoopback {
		return true
	}
	return false
}

type OpenAICompatibleAdapter struct {
	endpoint    *url.URL
	model       string
	apiKey      string
	client      *http.Client
	policy      EndpointPolicy
	diagnostics diagnosticLog
}

func newOpenAICompatibleAdapter(target Target, apiKey string) (*OpenAICompatibleAdapter, error) {
	return newOpenAICompatibleAdapterWithPolicy(target, apiKey, EndpointPolicy{})
}

func newOpenAICompatibleAdapterWithPolicy(target Target, apiKey string, policy EndpointPolicy) (*OpenAICompatibleAdapter, error) {
	return newOpenAICompatibleAdapterWithContext(context.Background(), target, apiKey, policy)
}

func newOpenAICompatibleAdapterWithContext(ctx context.Context, target Target, apiKey string, policy EndpointPolicy) (*OpenAICompatibleAdapter, error) {
	if target.Type != "openai_compatible" {
		return nil, fmt.Errorf("%s: unsupported target type", ErrEndpointInvalid)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("TARGET_AUTH_REQUIRED")
	}
	endpoint, err := validateEndpoint(ctx, target.Endpoint, policy)
	if err != nil {
		return nil, err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/chat/completions"
	client := policy.HTTPClient
	if client == nil {
		transport := &http.Transport{
			Proxy:                 nil,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			TLSClientConfig:       policy.TLSConfig,
			DisableKeepAlives:     false,
		}
		transport.DialContext = safeDialWithPolicy(policy)
		client = &http.Client{Transport: transport}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAICompatibleAdapter{endpoint: endpoint, model: target.Model, apiKey: apiKey, client: client, policy: policy}, nil
}

func resolveEndpointIPs(ctx context.Context, host string, policy EndpointPolicy) ([]net.IP, error) {
	if policy.Resolve != nil {
		return policy.Resolve(ctx, "ip", host)
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}
func safeDialContext(allowLoopback bool) func(context.Context, string, string) (net.Conn, error) {
	return safeDialWithPolicy(EndpointPolicy{AllowLoopback: allowLoopback})
}
func safeDialWithPolicy(policy EndpointPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else {
			ips, err = resolveEndpointIPs(ctx, host, policy)
			if err != nil {
				return nil, err
			}
		}
		for _, ip := range ips {
			if blockedIP(ip, policy.AllowLoopback) {
				return nil, fmt.Errorf("%s: mixed or restricted DNS", ErrSSRFBlocked)
			}
		}
		for _, ip := range ips {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, fmt.Errorf("%s: endpoint resolves to a restricted address", ErrSSRFBlocked)
	}
}

func (a *OpenAICompatibleAdapter) Request(ctx context.Context) (code string) {
	started := time.Now()
	diagnostic := RequestDiagnostic{}
	defer func() {
		if code == "" {
			return
		}
		now := time.Now().UTC()
		diagnostic.ErrorCode = code
		diagnostic.Count = 1
		diagnostic.FirstAt, diagnostic.LastAt = now, now
		diagnostic.LastLatencyMS = float64(time.Since(started)) / float64(time.Millisecond)
		a.diagnostics.record(diagnostic)
	}()
	body, err := json.Marshal(map[string]any{
		"model":      a.model,
		"max_tokens": 32,
		"messages":   []map[string]string{{"role": "user", "content": "health check"}},
	})
	if err != nil {
		return ErrResponseInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return ErrEndpointInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	response, err := a.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return ErrTargetTimeout
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return ErrTargetCanceled
		}
		if strings.Contains(err.Error(), ErrSSRFBlocked) {
			return ErrSSRFBlocked
		}
		diagnostic.FailureKind = networkFailure(err)
		return ErrConnectionFailed
	}
	defer response.Body.Close()
	diagnostic.HTTPStatus = response.StatusCode
	if response.StatusCode >= 300 {
		diagnostic.ProviderCode, diagnostic.Parameter = providerDiagnostic(response.Body)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return ErrRedirectBlocked
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrAuthFailed
	}
	if response.StatusCode == http.StatusRequestTimeout {
		return ErrTargetTimeout
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return ErrHTTP4xx
	}
	if response.StatusCode >= 500 {
		return ErrHTTP5xx
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrHTTP4xx
	}
	limited := io.LimitReader(response.Body, 1<<20)
	var result struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return ErrResponseInvalid
	}
	if len(result.Choices) == 0 {
		return ErrResponseInvalid
	}
	return ""
}

func (a *OpenAICompatibleAdapter) Close() { a.client.CloseIdleConnections() }
