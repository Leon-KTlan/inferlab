import { useEffect, useState } from 'react'
import { FileBarChart2, RefreshCw } from 'lucide-react'
import { api, type PublicReport as PublicReportData } from './api'

export default function PublicReport({ token }: { token: string }) {
  const [report, setReport] = useState<PublicReportData>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const load = async () => {
    setLoading(true); setError(''); setReport(undefined)
    try { setReport(await api.publicReport(token)) }
    catch { setError('报告不可用，链接可能已撤销或记录已删除。') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [token])
  return <main className="public-report"><header><strong>InferLab</strong><span>公开报告</span></header><h1><FileBarChart2 size={26} />脱敏体检报告</h1>{loading ? <p role="status">正在读取报告…</p> : error ? <div role="alert"><p>{error}</p><button className="secondary" onClick={load}><RefreshCw size={16} />重试</button></div> : report && <><p>{report.source_type === 'mock' ? 'Mock 实测，不代表真实模型服务性能。' : report.execution_mode === 'platform' ? '平台独立 Worker 采样。' : '本地 Runner 回传，未经服务端独立复测。'} · {report.status === 'succeeded' ? '已完成' : report.status === 'failed' ? '失败' : '已取消'}</p><div className="report-summary"><div className="metric"><span>总请求</span><strong>{report.metrics.request_count}</strong></div><div className="metric"><span>总体 P95</span><strong>{report.metrics.p95_e2e_ms.toFixed(2)} ms</strong></div><div className="metric"><span>错误率</span><strong>{(report.metrics.error_rate * 100).toFixed(2)}%</strong></div><div className="metric"><span>吞吐量</span><strong>{report.metrics.throughput_rps.toFixed(2)} req/s</strong></div></div><h2>阶段指标</h2>{report.stages.length ? <div className="table-wrap"><table><thead><tr><th>并发</th><th>P95 延迟</th><th>错误率</th><th>吞吐量</th></tr></thead><tbody>{report.stages.map(stage => <tr key={stage.stage}><td>{stage.concurrency}</td><td>{stage.p95_e2e_ms.toFixed(2)} ms</td><td>{(stage.error_rate * 100).toFixed(2)}%</td><td>{stage.throughput_rps.toFixed(2)} req/s</td></tr>)}</tbody></table></div> : <p>没有可用的阶段样本。</p>}<p>SLO：P95 ≤ {report.slo.p95_e2e_ms} ms，错误率 ≤ {(report.slo.error_rate_max * 100).toFixed(2)}%。{report.slo_status === 'pass' ? '采样达标。' : report.slo_status === 'fail' ? '至少一个阶段超限。' : '样本不足，无法形成容量结论。'}</p></>}</main>
}
