import { describe, expect, it } from 'vitest'
import { diagnosticAdvice, eventLabel } from './diagnostics'

describe('run diagnostics', () => {
  it('explains concrete status codes and known incompatible parameters', () => {
    expect(diagnosticAdvice({ error_code: 'TARGET_HTTP_4XX', http_status: 404 })).toContain('Base URL')
    expect(diagnosticAdvice({ error_code: 'TARGET_HTTP_4XX', http_status: 400, provider_code: 'unsupported_parameter', parameter: 'max_tokens' })).toContain('max_tokens')
    expect(diagnosticAdvice({ error_code: 'TARGET_RATE_LIMITED', http_status: 429, provider_code: 'insufficient_quota' })).toContain('额度不足')
    expect(diagnosticAdvice({ error_code: 'TARGET_CONNECTION_FAILED', failure_kind: 'tls_certificate' })).toContain('TLS 证书')
  })

  it('does not invent an exact status for old results', () => {
    const advice = diagnosticAdvice({ error_code: 'TARGET_HTTP_4XX' })
    expect(advice).toContain('没有具体状态码')
    expect(advice).not.toContain('400')
  })

  it('translates lifecycle events without losing the source semantics', () => {
    expect(eventLabel('local_runner_failed')).toContain('本地 Runner 执行失败')
    expect(eventLabel('stage_started')).toBe('阶段开始')
    expect(eventLabel('unknown_future_event')).toBe('unknown_future_event')
  })
})
