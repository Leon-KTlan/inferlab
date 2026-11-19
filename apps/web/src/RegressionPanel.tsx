import { Activity, Bookmark, RotateCcw, X } from 'lucide-react'
import { useRef, useState } from 'react'
import { api, ApiError, type Comparison, type Report, type Run } from './api'

const conflictMessages: Record<string, string> = {
  CONFIG_MISMATCH: '不可比较：来源、运行版本、目标、负载或 SLO 不一致。',
  INSUFFICIENT_SAMPLES: '证据不足：两次运行的每个阶段都需要至少 20 个完成样本。',
  INCOMPLETE_STAGES: '证据不足：至少一次运行未完成全部配置阶段。',
  RUN_NOT_SUCCEEDED: '证据不足：两次运行都必须成功完成。',
  RUN_NOT_FINISHED: '证据不足：两次运行都必须结束。',
  NOT_FOUND: '基线报告或实验已不可用，请重新选择基线。',
}

export default function RegressionPanel({ run, report, account, onRetest }: {
  run: Run
  report: Report
  account: string
  onRetest: (baseline: Report) => Promise<void>
}) {
  const key = `inferlab:regression-baseline:${account ? `user:${account}` : 'visitor'}`
  const [baseline, setBaseline] = useState(() => localStorage.getItem(key) ?? '')
  const [state, setState] = useState<'retest' | 'compare'>()
  const [result, setResult] = useState<Comparison>()
  const [error, setError] = useState('')
  const busy = useRef(false)
  const update = (id: string) => {
    if (id) localStorage.setItem(key, id)
    else localStorage.removeItem(key)
    setBaseline(id)
    setResult(undefined)
    setError('')
  }
  const execute = async (action: 'retest' | 'compare') => {
    if (!baseline || busy.current) return
    busy.current = true
    setState(action)
    setResult(undefined)
    setError('')
    try {
      if (action === 'retest') await onRetest(run.id === baseline ? report : await api.getReport(baseline))
      else setResult(await api.compareRuns(run.id, baseline))
    } catch (e) {
      setError(e instanceof ApiError && e.code && conflictMessages[e.code] ? conflictMessages[e.code] : e instanceof Error ? e.message : '操作失败，请重试。')
    } finally {
      busy.current = false
      setState(undefined)
    }
  }

  return <section className="report-panel compare-panel" aria-label="版本回归体检">
    <div className="panel-heading"><h3>版本回归体检</h3><span className="hint">基线保存在此浏览器</span></div>
    <p>{!baseline ? '尚未保存基线' : baseline === run.id ? '当前报告是基线' : <>已保存基线：<span style={{ overflowWrap: 'anywhere' }}>{baseline}</span></>}</p>
    <div className="beta-links">
      {run.status === 'succeeded' && baseline !== run.id && <button className="secondary" disabled={!!state} onClick={() => update(run.id)}><Bookmark size={16} />设为基线</button>}
      {baseline && <button className="secondary" disabled={!!state} onClick={() => void execute('retest')}><RotateCcw size={16} />{state === 'retest' ? '准备复测…' : '按基线复测'}</button>}
      {baseline && baseline !== run.id && <button className="secondary" disabled={!!state} onClick={() => void execute('compare')}><Activity size={16} />{state === 'compare' ? '正在对比…' : '对比已保存基线'}</button>}
      {baseline && <button className="text-button" disabled={!!state} onClick={() => update('')}><X size={16} />清除基线</button>}
    </div>
    {error && <p role="alert">{error}</p>}
    {result && <>
      <p role="status"><strong>{result.verdict === 'pass' ? '本次采样达标' : '本次采样未达标'}</strong>{result.failed_stages.length > 0 && ` · 超限阶段：${result.failed_stages.join('、')}`}</p>
      <div className="table-wrap"><table>
        <thead><tr><th scope="col">总体指标</th><th scope="col">基线</th><th scope="col">本次</th><th scope="col">阶段阈值</th></tr></thead>
        <tbody>
          <tr><th scope="row">P95</th><td>{result.overall.baseline.p95_e2e_ms.toFixed(2)} ms</td><td>{result.overall.compare.p95_e2e_ms.toFixed(2)} ms</td><td>≤ {result.slo.p95_e2e_ms} ms</td></tr>
          <tr><th scope="row">错误率</th><td>{(result.overall.baseline.error_rate * 100).toFixed(2)}%</td><td>{(result.overall.compare.error_rate * 100).toFixed(2)}%</td><td>≤ {+(result.slo.error_rate_max * 100).toFixed(4)}%</td></tr>
        </tbody>
      </table></div>
      <p className="compare-note">{result.notes[2]}</p>
    </>}
  </section>
}
