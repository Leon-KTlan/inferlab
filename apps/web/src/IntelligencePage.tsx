import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  BrainCircuit,
  CircleAlert,
  Download,
  FlaskConical,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  Square,
} from 'lucide-react'
import {
  downloadIntelligenceTask,
  intelligenceApi,
  intelligenceCommand,
  intelligenceRoute,
  intelligenceSource,
  intelligenceStatus,
  isComparableIntelligence,
  isIntelligenceActive,
  validateIntelligence,
  type IntelligenceInput,
  type IntelligenceRun,
  type IntelligenceSuite,
  type IntelligenceTask,
} from './intelligence-api'
import { RadarChart } from './RadarPage'

const initialInput: IntelligenceInput = {
  name: '能力检测 · canary-v1',
  target: { type: 'mock', model: 'mock-llm' },
  mock_profile: 'reference',
  repetitions: 2,
  authorized: false,
}
const errorMessage = (e: unknown) =>
  e instanceof Error ? e.message : '检测请求失败。'
const dateLabel = (value: string) => {
  const date = new Date(value)
  const today = new Date()
  if (date.toDateString() === today.toDateString()) return '今天'
  today.setDate(today.getDate() - 1)
  return date.toDateString() === today.toDateString()
    ? '昨天'
    : date.toLocaleDateString('zh-CN', {
        year: 'numeric',
        month: 'long',
        day: 'numeric',
      })
}

export default function IntelligencePage({
  onQuotaChange,
}: {
  onQuotaChange: () => void
}) {
  const [runs, setRuns] = useState<IntelligenceRun[]>([])
  const [suite, setSuite] = useState<IntelligenceSuite>()
  const [selected, setSelected] = useState(
    () => intelligenceRoute(window.location.hash)?.runId ?? '',
  )
  const [detail, setDetail] = useState<IntelligenceRun>()
  const [task, setTask] = useState<IntelligenceTask>()
  const [creating, setCreating] = useState(false)
  const [rerunId, setRerunId] = useState('')
  const [input, setInput] = useState<IntelligenceInput>(initialInput)
  const [key, setKey] = useState('')
  const [error, setError] = useState('')
  const [detailError, setDetailError] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const quotaRef = useRef(onQuotaChange)
  quotaRef.current = onQuotaChange
  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    const results = await Promise.allSettled([
      intelligenceApi.runs(),
      intelligenceApi.suite(),
    ])
    if (results[0].status === 'fulfilled') {
      setRuns(results[0].value)
      setLoaded(true)
    }
    if (results[1].status === 'fulfilled') setSuite(results[1].value)
    const failures = results.filter((item) => item.status === 'rejected')
    if (failures.length)
      setError(
        failures
          .map((item) =>
            item.status === 'rejected' ? errorMessage(item.reason) : '',
          )
          .join(' '),
      )
    setLoading(false)
  }, [])
  useEffect(() => {
    void load()
  }, [load])
  useEffect(() => {
    const sync = () => {
      setSelected(intelligenceRoute(window.location.hash)?.runId ?? '')
      setKey('')
      setCreating(false)
      setRerunId('')
      setInput((previous) => ({ ...previous, authorized: false }))
    }
    window.addEventListener('hashchange', sync)
    return () => window.removeEventListener('hashchange', sync)
  }, [])
  const open = (run: IntelligenceRun) => {
    setDetail(run)
    setDetailError('')
    setSelected(run.run_id)
    setCreating(false)
    setKey('')
    window.location.hash = `intelligence?run=${encodeURIComponent(run.run_id)}`
  }
  const upsert = (run: IntelligenceRun) =>
    setRuns((previous) => [
      run,
      ...previous.filter((item) => item.run_id !== run.run_id),
    ])
  useEffect(() => {
    if (!selected) {
      setDetail(undefined)
      setDetailError('')
      return
    }
    let canceled = false
    let timer: number | undefined
    setDetail((previous) =>
      previous?.run_id === selected ? previous : undefined,
    )
    const read = async () => {
      try {
        const next = await intelligenceApi.run(selected)
        if (canceled) return
        setDetail(next)
        setDetailError('')
        upsert(next)
        if (!isIntelligenceActive(next)) quotaRef.current()
        else timer = window.setTimeout(() => void read(), 3000)
      } catch (e) {
        if (!canceled) {
          setDetailError(errorMessage(e))
          timer = window.setTimeout(() => void read(), 5000)
        }
      }
    }
    void read()
    return () => {
      canceled = true
      window.clearTimeout(timer)
    }
  }, [selected])
  // Keep the timeline current even when no detail is open.
  const hasActive = !selected && runs.some(isIntelligenceActive)
  useEffect(() => {
    if (!hasActive) return
    const timer = window.setInterval(() => {
      if (!document.hidden)
        intelligenceApi
          .runs()
          .then((next) => {
            setRuns(next)
            if (!next.some(isIntelligenceActive)) quotaRef.current()
          })
          .catch((e) => setError(errorMessage(e)))
    }, 3000)
    return () => window.clearInterval(timer)
  }, [hasActive])
  const accept = (result: {
    run: IntelligenceRun
    task?: IntelligenceTask
  }) => {
    setTask(result.task)
    upsert(result.run)
    open(result.run)
    setRerunId('')
    if (result.task) downloadIntelligenceTask(result.task)
  }
  const submit = async () => {
    const validation = validateIntelligence(input)
    if (validation) {
      setError(validation)
      return
    }
    setBusy(true)
    setError('')
    const transientKey = key
    setKey('')
    try {
      accept(
        rerunId
          ? await intelligenceApi.rerun(rerunId, {
              authorized: !!input.authorized,
              api_key: transientKey || undefined,
            })
          : await intelligenceApi.create({
              ...input,
              name: input.name.trim(),
              target: {
                ...input.target,
                model: input.target.model.trim(),
                endpoint:
                  input.target.type === 'mock'
                    ? undefined
                    : input.target.endpoint?.trim(),
              },
              mock_profile:
                input.target.type === 'mock' ? input.mock_profile : undefined,
              api_key:
                input.target.type === 'mock'
                  ? undefined
                  : transientKey || undefined,
            }),
      )
      setInput((previous) => ({ ...previous, authorized: false }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
      quotaRef.current()
    }
  }
  const rerun = async (run: IntelligenceRun) => {
    setError('')
    setKey('')
    setTask(undefined)
    if (run.source_type !== 'mock') {
      setInput({
        name: run.config.name,
        target: run.config.target,
        repetitions: run.config.intelligence.repetitions,
        authorized: false,
      })
      setRerunId(run.run_id)
      setCreating(true)
      return
    }
    setBusy(true)
    try {
      accept(await intelligenceApi.rerun(run.run_id))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
      quotaRef.current()
    }
  }
  const cancel = async (run: IntelligenceRun) => {
    setBusy(true)
    setError('')
    try {
      const next = await intelligenceApi.cancel(run.run_id)
      upsert(next)
      setDetail(next)
      setTask(undefined)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
      quotaRef.current()
    }
  }
  const sorted = [...runs].sort(
    (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
  )
  const dates = [...new Set(sorted.map((run) => dateLabel(run.created_at)))]
  const selectedRun = detail?.run_id === selected ? detail : undefined
  return (
    <div className="content intelligence-page" data-testid="intelligence-page">
      <section className="intelligence-banner">
        <div>
          <span className="kicker">CAPABILITY CHECKUP / 01</span>
          <h2>从回答中，测量能力。</h2>
          <p>十道公开探针，五个观察维度。每一次检测，都留下可复核的证据。</p>
        </div>
        <BrainCircuit size={74} strokeWidth={1} aria-hidden="true" />
      </section>
      <div className="section-heading">
        <div>
          <span className="eyebrow">YOUR MEASUREMENTS</span>
          <h3>智力检测</h3>
        </div>
        <div className="intelligence-actions">
          <button
            className="secondary small"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw size={15} className={loading ? 'spin' : ''} />
            刷新
          </button>
          <button
            className="primary"
            data-testid="new-intelligence"
            onClick={() => {
              setCreating(true)
              setRerunId('')
              setInput(initialInput)
              setKey('')
              setError('')
            }}
          >
            <Plus size={16} />
            新建检测
          </button>
        </div>
      </div>
      <p className="intelligence-note">
        分数是 canary 题库通过率（0–100），不推算人类 IQ。Mock 仅演示评分；真实
        API 由你的本地 CLI 执行。
      </p>
      {error && (
        <div className="alert error" role="alert">
          <CircleAlert size={17} />
          <span>
            {error}
            {loaded && !creating ? ' 历史保留上次读取快照，可能已过期。' : ''}
          </span>
        </div>
      )}
      {creating && (
        <section
          className="form-panel intelligence-form"
          aria-label={rerunId ? '重新授权真实复测' : '新建智力检测'}
        >
          <div className="panel-heading">
            <div>
              <span className="eyebrow">
                {rerunId
                  ? 'REAUTHORIZE / SAME SNAPSHOT'
                  : 'CONTROLLED MEASUREMENT'}
              </span>
              <h3>{rerunId ? '重新授权，复测同一配置' : '建立一次能力检测'}</h3>
            </div>
            <ShieldCheck size={20} />
          </div>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void submit()
            }}
          >
            <fieldset disabled={busy}>
              <div className="segmented">
                <button
                  type="button"
                  className={input.target.type === 'mock' ? 'active' : ''}
                  disabled={!!rerunId}
                  onClick={() => {
                    setInput({
                      ...input,
                      target: { type: 'mock', model: 'mock-llm' },
                      mock_profile: 'reference',
                      authorized: false,
                    })
                    setKey('')
                  }}
                >
                  <FlaskConical size={16} />
                  Mock 模拟
                </button>
                <button
                  type="button"
                  className={
                    input.target.type === 'openai_compatible' ? 'active' : ''
                  }
                  disabled={!!rerunId}
                  onClick={() => {
                    setInput({
                      ...input,
                      target: {
                        type: 'openai_compatible',
                        model: '',
                        endpoint: '',
                      },
                      authorized: false,
                    })
                    setKey('')
                  }}
                >
                  真实 API
                </button>
              </div>
              <div className="field-grid">
                <label className="field">
                  <span>检测名称</span>
                  <input
                    required
                    value={input.name}
                    disabled={!!rerunId}
                    onChange={(e) =>
                      setInput({ ...input, name: e.target.value })
                    }
                  />
                </label>
                <label className="field">
                  <span>模型</span>
                  <input
                    required
                    aria-label="检测模型"
                    value={input.target.model}
                    disabled={!!rerunId}
                    onChange={(e) =>
                      setInput({
                        ...input,
                        target: { ...input.target, model: e.target.value },
                      })
                    }
                  />
                </label>
                <label className="field">
                  <span>题库</span>
                  <input readOnly value={suite?.suite_version ?? 'canary-v1'} />
                </label>
                <label className="field">
                  <span>每题重复次数</span>
                  <select
                    aria-label="每题重复次数"
                    disabled={!!rerunId}
                    value={input.repetitions}
                    onChange={(e) =>
                      setInput({
                        ...input,
                        repetitions: Number(e.target.value),
                      })
                    }
                  >
                    {[1, 2, 3].map((value) => (
                      <option key={value} value={value}>
                        {value} 次 · 共 {(suite?.probes.length ?? 10) * value}{' '}
                        请求
                      </option>
                    ))}
                  </select>
                </label>
                {input.target.type === 'mock' ? (
                  <label className="field">
                    <span>Mock 场景</span>
                    <select
                      aria-label="Mock 场景"
                      value={input.mock_profile ?? 'reference'}
                      onChange={(e) =>
                        setInput({
                          ...input,
                          mock_profile: e.target.value as
                            | 'reference'
                            | 'degraded',
                        })
                      }
                    >
                      <option value="reference">reference · 参考回答</option>
                      <option value="degraded">degraded · 部分错误演示</option>
                    </select>
                  </label>
                ) : (
                  <>
                    <label className="field">
                      <span>Endpoint · HTTPS</span>
                      <input
                        required
                        type="url"
                        placeholder="https://api.example.com/v1"
                        value={input.target.endpoint ?? ''}
                        disabled={!!rerunId}
                        onChange={(e) =>
                          setInput({
                            ...input,
                            target: {
                              ...input.target,
                              endpoint: e.target.value,
                            },
                          })
                        }
                      />
                    </label>
                    <label className="field">
                      <span>API Key · 可选，仅本次</span>
                      <input
                        type="password"
                        aria-label="智力检测 API Key"
                        autoComplete="off"
                        spellCheck={false}
                        maxLength={4096}
                        value={key}
                        onChange={(e) => setKey(e.target.value)}
                        placeholder="或在本地设置 OPENAI_API_KEY"
                      />
                    </label>
                  </>
                )}
              </div>
              {input.target.type !== 'mock' && (
                <label className="checkbox">
                  <input
                    required
                    type="checkbox"
                    checked={!!input.authorized}
                    onChange={(e) =>
                      setInput({ ...input, authorized: e.target.checked })
                    }
                  />
                  我拥有此目标的测试授权，并理解真实请求可能产生流量与费用。
                </label>
              )}
              <p className="intelligence-note">
                {input.target.type === 'mock'
                  ? '模拟来源 · 本地 fixture 实际评分，不访问外部模型。'
                  : '实测来源 · API Key 只在页面内存中使用；可临时交接至服务端内存 5 分钟，单次领取，不写入 task.json。'}{' '}
                不保存原始回答或敏感响应。单并发，最多 30 请求、1 req/s、120
                秒；不执行生成代码。
              </p>
              <div className="form-actions">
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    setCreating(false)
                    setKey('')
                    setRerunId('')
                    setInput((previous) => ({ ...previous, authorized: false }))
                  }}
                >
                  收起表单
                </button>
                <button
                  className="primary"
                  type="submit"
                  disabled={busy || !suite}
                >
                  <Play size={16} />
                  {busy
                    ? '正在创建…'
                    : input.target.type === 'mock'
                      ? '开始 Mock 检测'
                      : '授权并创建本地任务'}
                </button>
              </div>
            </fieldset>
          </form>
        </section>
      )}
      {selected && (
        <section className="intelligence-detail" aria-label="智力检测详情">
          <div className="intelligence-detail-heading">
            <button
              className="text-button"
              onClick={() => {
                window.location.hash = 'intelligence'
                setSelected('')
                setDetail(undefined)
              }}
            >
              <ArrowLeft size={15} />
              返回检测历史
            </button>
            <a className="text-button" href="#radar">
              查看智商雷达 <ArrowRight size={15} />
            </a>
          </div>
          {detailError && (
            <div className="alert error" role="alert">
              <CircleAlert size={16} />
              {detailError}
              {selectedRun ? ' 当前为上次快照，可能已过期。' : ''}
            </div>
          )}
          {!selectedRun && !detailError && (
            <p role="status">正在读取检测详情…</p>
          )}
          {selectedRun && (
            <>
              <section className="form-panel">
                <div className="panel-heading">
                  <div>
                    <span className="eyebrow">
                      MEASUREMENT / {selectedRun.run_id}
                    </span>
                    <h3>{selectedRun.config.name}</h3>
                  </div>
                  <span className="mock-badge">
                    {intelligenceSource(selectedRun.source_type)}
                  </span>
                </div>
                <dl className="snapshot-grid">
                  <div>
                    <dt>模型 / 目标</dt>
                    <dd>
                      {selectedRun.config.target.model}
                      <br />
                      {selectedRun.config.target.endpoint ?? 'Mock 本地模拟'}
                    </dd>
                  </div>
                  <div>
                    <dt>题库 / 重复次数</dt>
                    <dd>
                      {selectedRun.config.intelligence.suite_version} ·{' '}
                      {selectedRun.config.intelligence.repetitions} 次
                    </dd>
                  </div>
                  <div>
                    <dt>状态 / Mock profile</dt>
                    <dd>
                      {intelligenceStatus(selectedRun.status)}
                      {selectedRun.source_type === 'mock'
                        ? ` · ${selectedRun.config.intelligence.mock_profile}`
                        : ''}
                    </dd>
                  </div>
                  <div>
                    <dt>创建时间</dt>
                    <dd>
                      {new Date(selectedRun.created_at).toLocaleString('zh-CN')}
                    </dd>
                  </div>
                </dl>
                <div className="intelligence-actions detail-actions">
                  {isIntelligenceActive(selectedRun) ? (
                    <button
                      className="danger-button"
                      disabled={busy}
                      onClick={() => void cancel(selectedRun)}
                    >
                      <Square size={14} />
                      取消检测
                    </button>
                  ) : (
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => void rerun(selectedRun)}
                    >
                      <RotateCcw size={15} />
                      {selectedRun.source_type === 'mock'
                        ? '同快照复测'
                        : '重新授权并复测'}
                    </button>
                  )}
                </div>
                {selectedRun.error_code && (
                  <p className="intelligence-note" role="status">
                    运行错误：{selectedRun.error_code}
                    。传输错误不表示模型能力退化。
                  </p>
                )}
                {selectedRun.status === 'canceled' && (
                  <p className="intelligence-note">
                    此检测已取消；未完成题目不计通过，不参与雷达基线。
                  </p>
                )}
              </section>
              {selectedRun.source_type !== 'mock' &&
                isIntelligenceActive(selectedRun) && (
                  <TaskHandoff
                    task={
                      task?.run_id === selectedRun.run_id ? task : undefined
                    }
                  />
                )}
              <Measurement run={selectedRun} suite={suite} />
            </>
          )}
        </section>
      )}
      {!selected && (
        <>
          {loading && (
            <p role="status" className="intelligence-note">
              正在读取检测历史…
            </p>
          )}
          {loaded && !sorted.length && (
            <div className="empty-state">
              <BrainCircuit size={30} />
              <h4>第一条能力曲线，从这里开始</h4>
              <p>选择 Mock 参考场景即可体验完整的评分、逐题复核与雷达流程。</p>
              <button
                className="secondary"
                onClick={() => {
                  setInput(initialInput)
                  setCreating(true)
                }}
              >
                新建第一次检测
              </button>
            </div>
          )}
          {dates.map((date) => (
            <section className="intelligence-day" key={date}>
              <h4>
                <span />
                {date}
                <small>
                  {
                    sorted.filter((run) => dateLabel(run.created_at) === date)
                      .length
                  }{' '}
                  次检测
                </small>
              </h4>
              <div className="intelligence-history">
                {sorted
                  .filter((run) => dateLabel(run.created_at) === date)
                  .map((run) => (
                    <button
                      className="intelligence-card"
                      key={run.run_id}
                      onClick={() => open(run)}
                      data-testid="intelligence-history-card"
                    >
                      <div className="intelligence-card-top">
                        <span className="mock-badge">
                          {run.source_type === 'mock'
                            ? `Mock · ${run.config.intelligence.mock_profile}`
                            : '实测 · 本地 CLI'}
                        </span>
                        <span
                          className={`status ${run.status === 'succeeded' ? 'success' : run.status === 'running' || run.status === 'queued' ? 'active' : 'danger'}`}
                        >
                          <span />
                          {intelligenceStatus(run.status)}
                        </span>
                      </div>
                      <h4>{run.config.name}</h4>
                      <p>
                        {run.config.target.model} ·{' '}
                        {run.config.intelligence.suite_version} · 每题{' '}
                        {run.config.intelligence.repetitions} 次
                      </p>
                      <div className="intelligence-card-bottom">
                        <strong>
                          {run.intelligence_report
                            ? run.intelligence_report.score.toFixed(1)
                            : '—'}
                          <small>/ 100</small>
                        </strong>
                        <span>
                          {run.intelligence_report?.complete
                            ? '完整检测'
                            : isIntelligenceActive(run)
                              ? '等待结果'
                              : '未完成 · 不可比较'}
                          <br />
                          <time>
                            {new Date(run.created_at).toLocaleTimeString(
                              'zh-CN',
                              { hour: '2-digit', minute: '2-digit' },
                            )}
                          </time>
                        </span>
                        <ArrowRight size={17} />
                      </div>
                    </button>
                  ))}
              </div>
            </section>
          ))}
        </>
      )}
    </div>
  )
}

function TaskHandoff({ task }: { task?: IntelligenceTask }) {
  const [copyStatus, setCopyStatus] = useState('')
  const command = intelligenceCommand(window.location.origin)
  return (
    <section
      className="events-panel runner-handoff"
      aria-label="智力检测本地 Runner"
    >
      <div className="panel-heading">
        <div>
          <span className="eyebrow">LOCAL EXECUTION</span>
          <h3>{task ? '下载任务，在本地开始实测' : '任务令牌已从页面清除'}</h3>
        </div>
        <Download size={18} />
      </div>
      {task ? (
        <>
          <p className="handoff-copy">
            task.json 含一次性任务凭据，请妥善保管。刷新或离开后无法重新下载。
            {task.credential_available
              ? '请在 5 分钟内启动 Runner 领取临时 API Key。'
              : '启动前请在本地设置 OPENAI_API_KEY。'}
          </p>
          <button
            className="secondary task-download"
            onClick={() => downloadIntelligenceTask(task)}
          >
            <Download size={16} />
            下载智力检测 task.json
          </button>
          <p className="handoff-copy">
            在项目根目录运行，将 /path/task.json 替换为下载文件的实际路径。
          </p>
          <div className="command-row">
            <code>{command}</code>
            <button
              className="secondary"
              onClick={() => {
                navigator.clipboard
                  .writeText(command)
                  .then(() => setCopyStatus('命令已复制'))
                  .catch(() => setCopyStatus('复制失败，请手动复制命令。'))
              }}
            >
              复制命令
            </button>
          </div>
          <p className="intelligence-note" role="status">
            {copyStatus}
          </p>
        </>
      ) : (
        <p className="handoff-copy">
          刷新后任务令牌无法恢复。若已下载 task.json，可继续执行；若 Runner
          已启动，请等待回传。否则先取消旧记录，再重新授权创建任务并下载。
        </p>
      )}
      <p className="intelligence-note">
        网页取消只关闭运行记录；停止真实目标请求需在终端按 Ctrl+C。
      </p>
    </section>
  )
}

function Measurement({
  run,
  suite,
}: {
  run: IntelligenceRun
  suite?: IntelligenceSuite
}) {
  const report = run.intelligence_report
  const planned =
    (suite?.probes.length ?? 10) * run.config.intelligence.repetitions
  const completed = report?.completed ?? run.completed_count ?? 0
  return (
    <>
      <section className="form-panel">
        <div className="panel-heading">
          <div>
            <span className="eyebrow">MEASUREMENT RESULT</span>
            <h3>{report?.complete ? '能力分布' : '检测进度 · 未完成'}</h3>
          </div>
          <span className="hint">
            {isComparableIntelligence(run)
              ? '完整结果可参与同配置比较'
              : report?.error_count || run.error_code
                ? '存在请求错误，不能比较'
                : !report?.complete
                  ? '未完成，不能比较'
                  : '当前状态不能比较'}
          </span>
        </div>
        <div
          className="progress-track"
          role="progressbar"
          aria-label="检测完成度"
          aria-valuenow={completed}
          aria-valuemin={0}
          aria-valuemax={report?.total ?? planned}
        >
          <span
            style={{
              width: `${Math.min(100, (completed / (report?.total ?? planned)) * 100)}%`,
            }}
          />
        </div>
        <div className="metric-grid">
          <div className="metric">
            <span>通过率 / 100</span>
            <strong>{report?.score.toFixed(1) ?? '—'}</strong>
          </div>
          <div className="metric">
            <span>完成 / 计划</span>
            <strong>
              {completed} / {report?.total ?? planned}
            </strong>
          </div>
          <div className="metric">
            <span>传输错误</span>
            <strong>{report?.error_count ?? '—'}</strong>
          </div>
          <div className="metric">
            <span>运行耗时</span>
            <strong>
              {Number.isFinite(run.duration_seconds)
                ? `${run.duration_seconds.toFixed(1)} s`
                : '—'}
            </strong>
          </div>
        </div>
        {report && (
          <>
            <p className="intelligence-note">
              {report.conclusion} 错误与未完成均不获得分数。
            </p>
            <RadarChart dimensions={report.dimensions} />
          </>
        )}
      </section>
      <section className="form-panel">
        <div className="panel-heading">
          <div>
            <span className="eyebrow">PUBLIC PROBES / AUDIT</span>
            <h3>逐题复核</h3>
          </div>
          <span className="hint">只记录结果与耗时，不保存原始回答</span>
        </div>
        {!suite ? (
          <p className="intelligence-note">
            题库未能读取。请使用刷新按钮重试，题目和期望答案以服务端题库为准。
          </p>
        ) : suite.suite_version !== run.config.intelligence.suite_version ? (
          <p className="intelligence-note">
            当前公开题库版本与此记录不同，无法展示匹配的题目；请核对该版本题库。
          </p>
        ) : (
          <div className="probe-list">
            {suite.probes.map((probe) => (
              <details key={probe.id}>
                <summary>
                  <span>
                    {probe.title}
                    <small>
                      {suite.dimensions.find(
                        (dimension) => dimension.id === probe.dimension,
                      )?.label ?? probe.dimension}{' '}
                      · {probe.id}
                    </small>
                  </span>
                  <span className="probe-result">
                    {report?.outcomes?.filter(
                      (item) => item.probe_id === probe.id && item.passed,
                    ).length ?? 0}{' '}
                    / {run.config.intelligence.repetitions} 通过
                  </span>
                </summary>
                <div className="probe-body">
                  <dl>
                    <dt>题目</dt>
                    <dd>
                      <pre>{probe.prompt}</pre>
                    </dd>
                    <dt>期望 / 评分规则</dt>
                    <dd>
                      <pre>{probe.expected}</pre>
                    </dd>
                  </dl>
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>轮次</th>
                          <th>结果</th>
                          <th>耗时</th>
                          <th>错误码</th>
                        </tr>
                      </thead>
                      <tbody>
                        {Array.from(
                          { length: run.config.intelligence.repetitions },
                          (_, i) => {
                            const outcome = report?.outcomes?.find(
                              (item) =>
                                item.probe_id === probe.id &&
                                item.repetition === i + 1,
                            )
                            return (
                              <tr key={i}>
                                <td>{i + 1}</td>
                                <td>
                                  {!outcome
                                    ? '未完成'
                                    : outcome.error_code
                                      ? '请求错误'
                                      : outcome.passed
                                        ? '通过'
                                        : '未通过'}
                                </td>
                                <td>
                                  {outcome
                                    ? `${outcome.latency_ms.toFixed(1)} ms`
                                    : '—'}
                                </td>
                                <td>{outcome?.error_code ?? '—'}</td>
                              </tr>
                            )
                          },
                        )}
                      </tbody>
                    </table>
                  </div>
                </div>
              </details>
            ))}
          </div>
        )}
      </section>
    </>
  )
}
