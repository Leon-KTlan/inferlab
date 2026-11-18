export interface RequestDiagnostic {
  error_code: string
  http_status?: number
  provider_code?: string
  parameter?: string
  failure_kind?: string
  count: number
  first_at: string
  last_at: string
  last_latency_ms: number
}

export function diagnosticAdvice(d: Pick<RequestDiagnostic, 'error_code' | 'http_status' | 'provider_code' | 'parameter' | 'failure_kind'>): string {
  switch (d.provider_code) {
    case 'model_not_found': return '上游未找到模型。检查模型名称，以及该 Key 的模型访问权限。'
    case 'unsupported_parameter': case 'unsupported_value': case 'invalid_parameter': return `上游拒绝${d.parameter ? ` ${d.parameter} ` : '请求'}参数。检查所用模型是否支持该参数；当前探测使用 max_tokens=32。`
    case 'insufficient_quota': return '上游报告额度不足。检查账户余额、项目额度和计费状态。'
    case 'context_length_exceeded': return '上游报告上下文超限。检查模型上下文限制。'
  }
  switch (d.failure_kind) {
    case 'dns': return 'DNS 解析失败。检查目标域名和本机 DNS 配置。'
    case 'tls_certificate': return 'TLS 证书校验失败。检查证书链、域名和系统时间。'
    case 'tls_protocol': return 'TLS 协议不匹配。确认目标端口提供 HTTPS 服务。'
    case 'connection_refused': return '连接被拒绝。检查服务监听端口及防火墙。'
    case 'network_unreachable': return '网络不可达。检查本机路由及网络连接。'
    case 'connection': return '无法连接目标。检查本机网络、目标服务与防火墙。'
  }
  switch (d.http_status) {
    case 400: case 422: return '上游拒绝请求格式或参数。核对模型名称、Chat Completions 协议及 max_tokens 支持情况。'
    case 401: return 'API Key 未通过认证。检查 Key 是否有效，以及是否属于当前 Endpoint 的服务商。'
    case 403: return '上游拒绝访问。检查模型权限、项目权限或 IP 访问限制。'
    case 404: return '上游未找到接口或模型。检查 Base URL（不含 /chat/completions）和模型名称。'
    case 405: return '目标不支持 POST。检查 Endpoint 是否为聊天补全 API 的 Base URL。'
    case 408: return '上游返回请求超时。检查目标服务耗时。'
    case 429: return '请求被限流或额度耗尽。降低 RPS，并检查服务商余额和配额。'
  }
  if (d.http_status && d.http_status >= 500) return '上游服务或网关异常。检查服务商状态，稍后使用小请求预算复测。'
  switch (d.error_code) {
    case 'TARGET_HTTP_4XX': return '上游返回 4xx。此记录没有具体状态码；核对接口、模型和参数后重新小量测试。'
    case 'TARGET_HTTP_5XX': return '上游服务或网关异常。检查服务商状态。'
    case 'TARGET_AUTH_FAILED': case 'TARGET_AUTH_REQUIRED': return '认证失败或缺少 Key。检查 Key 和模型访问权限。'
    case 'TARGET_RATE_LIMITED': return '请求被限流或额度受限。降低 RPS，检查账户额度。'
    case 'TARGET_TIMEOUT': return '请求达到单请求超时或阶段截止。检查超时、阶段时长及目标服务耗时。'
    case 'TARGET_ENDPOINT_INVALID': return '目标地址或 DNS 校验失败。检查 HTTPS 地址与域名解析。'
    case 'TARGET_SSRF_BLOCKED': return '目标被地址安全策略拒绝。仅允许已授权的公网 HTTPS 地址。'
    case 'TARGET_REDIRECT_BLOCKED': return '目标返回重定向。请填写最终 API Base URL。'
    case 'TARGET_RESPONSE_INVALID': return '响应不符合 Chat Completions JSON 格式。检查接口兼容性和网关配置。'
    case 'TARGET_CONNECTION_FAILED': return '连接失败。检查网络、TLS 证书和目标服务。'
    case 'RUN_CANCELED': case 'TARGET_CANCELED': return '请求已取消。已发出的请求可能仍产生费用。'
    default: return '检查目标服务与任务配置；当前记录没有更详细的上游诊断。'
  }
}

export function eventLabel(type: string): string {
  return ({ local_runner_task_created: '本地任务已创建，等待 Runner 回传', local_runner_succeeded: '本地 Runner 执行完成，结果已接收', local_runner_failed: '本地 Runner 执行失败，结果已接收', local_runner_canceled: '本地 Runner 已取消', run_canceled: '运行记录已取消', run_created: '运行已创建', run_started: '开始执行', stage_started: '阶段开始', stage_finished: '阶段结束', run_succeeded: '运行完成', run_failed: '运行失败' } as Record<string, string>)[type] ?? type
}
