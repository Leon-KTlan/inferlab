package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"
)

const maxDiagnostics = 16

type RequestDiagnostic struct {
	ErrorCode     string    `json:"error_code"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	ProviderCode  string    `json:"provider_code,omitempty"`
	Parameter     string    `json:"parameter,omitempty"`
	FailureKind   string    `json:"failure_kind,omitempty"`
	Count         int       `json:"count"`
	FirstAt       time.Time `json:"first_at"`
	LastAt        time.Time `json:"last_at"`
	LastLatencyMS float64   `json:"last_latency_ms"`
}

// Only known protocol symbols survive. Upstream text can echo credentials or prompts.
func safeProviderCode(value string) string {
	switch value {
	case "invalid_api_key", "authentication_error", "permission_denied", "model_not_found", "unsupported_parameter", "unsupported_value", "invalid_request_error", "invalid_parameter", "insufficient_quota", "rate_limit_exceeded", "context_length_exceeded", "content_policy_violation", "server_error":
		return value
	}
	return ""
}

func safeParameter(value string) string {
	switch value {
	case "model", "messages", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stream", "response_format", "tools", "tool_choice":
		return value
	}
	return ""
}

func providerDiagnostic(body io.Reader) (string, string) {
	var response struct {
		Error struct {
			Code      json.RawMessage `json:"code"`
			Type      string          `json:"type"`
			Parameter string          `json:"param"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(body, 16<<10)).Decode(&response) != nil {
		return "", ""
	}
	var code string
	_ = json.Unmarshal(response.Error.Code, &code)
	code = safeProviderCode(code)
	if code == "" {
		code = safeProviderCode(response.Error.Type)
	}
	return code, safeParameter(response.Error.Parameter)
}

func networkFailure(err error) string {
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	switch {
	case errors.As(err, &dns):
		return "dns"
	case errors.As(err, &certificate), errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid):
		return "tls_certificate"
	case errors.As(err, &record):
		return "tls_protocol"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network_unreachable"
	default:
		return "connection"
	}
}

type diagnosticLog struct {
	mu      sync.Mutex
	items   []RequestDiagnostic
	onFirst func(RequestDiagnostic)
}

func (l *diagnosticLog) record(d RequestDiagnostic) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, current := range l.items {
		if current.ErrorCode == d.ErrorCode && current.HTTPStatus == d.HTTPStatus && current.ProviderCode == d.ProviderCode && current.Parameter == d.Parameter && current.FailureKind == d.FailureKind {
			l.items[i].Count++
			if d.LastAt.After(current.LastAt) {
				l.items[i].LastAt = d.LastAt
				l.items[i].LastLatencyMS = d.LastLatencyMS
			}
			if d.FirstAt.Before(current.FirstAt) {
				l.items[i].FirstAt = d.FirstAt
			}
			return
		}
	}
	if len(l.items) >= maxDiagnostics {
		return
	}
	l.items = append(l.items, d)
	if l.onFirst != nil {
		l.onFirst(d)
	}
}

func (l *diagnosticLog) snapshot() []RequestDiagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	items := append([]RequestDiagnostic(nil), l.items...)
	sort.Slice(items, func(i, j int) bool { return items[i].FirstAt.Before(items[j].FirstAt) })
	return items
}

func validateDiagnostics(items []RequestDiagnostic, metrics Metrics, started, finished *time.Time) error {
	if len(items) > maxDiagnostics {
		return fmt.Errorf("too many diagnostic groups")
	}
	total := 0
	for _, d := range items {
		if !isRunnerErrorCode(d.ErrorCode) || d.Count < 1 || d.Count > metrics.ErrorCount || d.HTTPStatus != 0 && (d.HTTPStatus < 100 || d.HTTPStatus > 599) || safeProviderCode(d.ProviderCode) != d.ProviderCode || safeParameter(d.Parameter) != d.Parameter {
			return fmt.Errorf("invalid diagnostic classification or count")
		}
		switch d.FailureKind {
		case "", "dns", "tls_certificate", "tls_protocol", "connection_refused", "network_unreachable", "connection":
		default:
			return fmt.Errorf("invalid diagnostic network category")
		}
		if d.FirstAt.IsZero() || d.LastAt.Before(d.FirstAt) || started == nil || finished == nil || d.FirstAt.Before(*started) || d.LastAt.After(*finished) || math.IsNaN(d.LastLatencyMS) || math.IsInf(d.LastLatencyMS, 0) || d.LastLatencyMS < 0 || d.LastLatencyMS > finished.Sub(*started).Seconds()*1000+50 {
			return fmt.Errorf("invalid diagnostic observation window")
		}
		total += d.Count
	}
	if total > metrics.ErrorCount {
		return fmt.Errorf("diagnostic counts exceed failed requests")
	}
	return nil
}

func diagnosticHint(d RequestDiagnostic) string {
	switch d.ProviderCode {
	case "model_not_found":
		return "上游未找到模型；检查模型名称及该 Key 的模型访问权限。"
	case "unsupported_parameter", "unsupported_value", "invalid_parameter":
		return "上游拒绝请求参数；检查所列参数与模型接口是否兼容。"
	case "insufficient_quota":
		return "上游报告额度不足；检查账户余额和项目额度。"
	case "context_length_exceeded":
		return "上游报告上下文超限；检查模型上下文限制。"
	}
	if d.FailureKind != "" {
		switch d.FailureKind {
		case "dns":
			return "DNS 解析失败；检查域名与本机 DNS。"
		case "tls_certificate":
			return "TLS 证书校验失败；检查证书链、域名与系统时间。"
		case "tls_protocol":
			return "TLS 握手协议不匹配；确认目标端口提供 HTTPS。"
		case "connection_refused":
			return "连接被拒绝；检查服务监听端口和防火墙。"
		case "network_unreachable":
			return "网络不可达；检查本机路由和网络。"
		default:
			return "连接失败；检查网络、目标服务和防火墙。"
		}
	}
	switch d.HTTPStatus {
	case 400, 422:
		return "上游拒绝请求格式或参数；核对模型、接口协议与 max_tokens 支持情况。"
	case 401:
		return "上游拒绝认证；检查 API Key 是否有效及是否属于此服务。"
	case 403:
		return "上游拒绝访问；检查模型权限、项目或 IP 限制。"
	case 404:
		return "上游未找到接口或模型；检查 Base URL（不含 /chat/completions）及模型名称。"
	case 405:
		return "上游不支持 POST；检查 Base URL 是否为聊天补全 API。"
	case 408:
		return "上游返回请求超时；检查目标服务耗时。"
	case 429:
		return "上游限制请求或额度；降低 RPS，并检查账户额度。"
	}
	if d.HTTPStatus >= 500 {
		return "上游服务或网关异常；稍后在小请求预算下复测。"
	}
	switch d.ErrorCode {
	case ErrHTTP4xx:
		return "上游返回 4xx；旧记录无具体状态码，请核对接口、模型和参数后重新小量测试。"
	case ErrTargetTimeout:
		return "请求达到超时或阶段截止；检查单请求超时、阶段时长及服务耗时。"
	case ErrAuthFailed, "TARGET_AUTH_REQUIRED":
		return "认证失败或缺少 Key；检查凭据和模型访问权限。"
	case ErrSSRFBlocked:
		return "目标被地址安全策略拒绝；仅允许授权的公网 HTTPS 服务。"
	case ErrRedirectBlocked:
		return "上游返回重定向；请填写最终 API Base URL。"
	case ErrResponseInvalid:
		return "响应不是预期的 Chat Completions JSON；检查接口兼容性。"
	case "RUN_CANCELED", ErrTargetCanceled:
		return "运行已取消；已发送的请求可能仍产生费用。"
	case ErrEndpointInvalid:
		return "目标地址或 DNS 校验失败；检查 HTTPS 地址和解析结果。"
	default:
		return "请按错误分类检查目标服务、网络与任务配置。"
	}
}

func printDiagnostic(w io.Writer, d RequestDiagnostic, phase string) {
	fmt.Fprintf(w, "%s [%s] %s http=%d provider=%s param=%s network=%s count=%d latency_ms=%.2f\n  %s\n", d.LastAt.Format(time.RFC3339), phase, d.ErrorCode, d.HTTPStatus, d.ProviderCode, d.Parameter, d.FailureKind, d.Count, d.LastLatencyMS, diagnosticHint(d))
}
