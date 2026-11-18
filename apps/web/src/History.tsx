import { useEffect, useState } from 'react'
import { FileBarChart2, LoaderCircle, RefreshCw, Trash2 } from 'lucide-react'
import { api, type Run } from './api'

export default function History({ onOpen, onDelete }: { onOpen: (run: Run) => void; onDelete: () => void }) {
  const [runs, setRuns] = useState<Run[]>([])
  const [experiments, setExperiments] = useState<Awaited<ReturnType<typeof api.listExperiments>>>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const load = async () => {
    setLoading(true); setError('')
    try { await api.me(); const [nextRuns, nextExperiments] = await Promise.all([api.listRuns(), api.listExperiments()]); setRuns(nextRuns); setExperiments(nextExperiments) }
    catch (e) { setError(e instanceof Error ? e.message : '历史读取失败。') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])
  const remove = async (id: string, experiment: boolean) => {
    if (!window.confirm(experiment ? '删除此实验及全部终态运行和关联分享？此操作不可恢复，预留额度不退还。' : '删除此运行及关联分享？此操作不可恢复，预留额度不退还。')) return
    setBusy(id); setError('')
    try { if (experiment) await api.deleteExperiment(id); else await api.deleteRun(id); onDelete(); await load() }
    catch (e) { setError(e instanceof Error ? e.message : '删除失败。') }
    finally { setBusy('') }
  }
  return <section className="history-band" aria-label="历史管理"><div className="panel-heading"><h3>全部实验与运行</h3><button title="刷新历史" aria-label="刷新历史" onClick={load} disabled={loading}><RefreshCw size={17} /></button></div>{error && <p className="alert error" role="alert">{error}</p>}{loading ? <p><LoaderCircle className="spin" size={16} />正在读取历史…</p> : !experiments.length ? <p className="inline-empty">暂无实验记录。</p> : experiments.map(experiment => {
    const children = runs.filter(run => run.experiment_id === experiment.experiment_id)
    const active = children.some(run => run.status === 'queued' || run.status === 'running')
    return <div className="history-experiment" key={experiment.experiment_id}><div className="history-heading"><strong>{experiment.name}</strong><button title={active ? '活跃运行结束后可删除实验' : '删除实验'} aria-label={`删除实验 ${experiment.name}`} disabled={active || !!busy} onClick={() => remove(experiment.experiment_id, true)}><Trash2 size={17} /></button></div>{children.length ? children.map(run => <div className="history-row" key={run.id}><button className="text-button history-open" onClick={() => onOpen(run)}><FileBarChart2 size={16} /><span>{run.id}</span></button><span>{({ queued: '排队中', running: '执行中', succeeded: '已完成', failed: '失败', canceled: '已取消' })[run.status]}</span><button title="删除运行" aria-label={`删除运行 ${run.id}`} disabled={run.status === 'queued' || run.status === 'running' || !!busy} onClick={() => remove(run.id, false)}><Trash2 size={16} /></button></div>) : <p className="inline-empty">尚无运行。</p>}</div>
  })}</section>
}
