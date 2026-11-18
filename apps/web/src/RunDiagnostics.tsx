import { CircleAlert } from 'lucide-react'
import type { Run } from './api'
import { diagnosticAdvice } from './diagnostics'

function observedAt(value: string) {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

export default function RunDiagnostics({ run }: { run: Run }) {
  const diagnostics = run.diagnostics ?? []
  if (!run.error && diagnostics.length === 0) return null
  return <section className="events-panel diagnostic-panel" aria-label="错误诊断">
    <div className="panel-heading"><div><span className="eyebrow">ERROR DIAGNOSTICS</span><h3>错误诊断</h3></div><CircleAlert size={18} /></div>
    {diagnostics.length === 0 ? <><code className="diagnostic-code">{run.error}</code><p className="diagnostic-advice">{diagnosticAdvice({ error_code: run.error ?? '' })}</p></> : diagnostics.map((d, index) => <article className="diagnostic-item" key={index}>
      <div className="diagnostic-heading"><strong>{d.http_status ? `HTTP ${d.http_status}` : '网络或协议错误'}</strong><span>{d.count} 次</span></div>
      <code className="diagnostic-code">{d.error_code}</code>
      <p className="diagnostic-advice">{diagnosticAdvice(d)}</p>
      <dl className="diagnostic-details">
        {d.provider_code && <div><dt>上游错误类型</dt><dd><code>{d.provider_code}</code></dd></div>}
        {d.parameter && <div><dt>相关参数</dt><dd><code>{d.parameter}</code></dd></div>}
        {d.failure_kind && <div><dt>连接分类</dt><dd><code>{d.failure_kind}</code></dd></div>}
        <div><dt>最近请求耗时</dt><dd>{d.last_latency_ms.toFixed(2)} ms</dd></div>
        <div><dt>首次发生</dt><dd>{observedAt(d.first_at)}</dd></div>
        <div><dt>最后发生</dt><dd>{observedAt(d.last_at)}</dd></div>
      </dl>
    </article>)}
  </section>
}
