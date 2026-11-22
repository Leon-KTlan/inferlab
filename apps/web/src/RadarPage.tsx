import { useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowRight, RefreshCw, Radar, CircleAlert } from 'lucide-react'
import {
  intelligenceApi,
  intelligenceSource,
  intelligenceStatus,
  isComparableIntelligence,
  type IntelligenceDimension,
  type RadarGroup,
} from './intelligence-api'

const fallbackDimensions = [
  { id: 'reasoning', label: '逻辑推理' },
  { id: 'math', label: '数学计算' },
  { id: 'instruction', label: '指令遵循' },
  { id: 'structured', label: '结构化输出' },
  { id: 'context', label: '上下文检索' },
]
const point = (index: number, score: number) => {
  const angle = ((index * 72 - 90) * Math.PI) / 180
  return [
    180 + (Math.cos(angle) * 112 * score) / 100,
    160 + (Math.sin(angle) * 112 * score) / 100,
  ]
}
export function RadarChart({
  dimensions,
  baseline,
}: {
  dimensions: IntelligenceDimension[]
  baseline?: IntelligenceDimension[]
}) {
  const axes = fallbackDimensions.map((axis) => ({
    ...axis,
    ...dimensions.find((item) => item.id === axis.id),
  }))
  const polygon = (values: number[]) =>
    values
      .map((value, i) => point(i, Math.max(0, Math.min(100, value))).join(','))
      .join(' ')
  return (
    <div className="intelligence-chart">
      <svg
        viewBox="0 0 360 320"
        role="img"
        aria-label="五维能力雷达图，具体分数见下方数值表"
      >
        {[20, 40, 60, 80, 100].map((value) => (
          <polygon
            key={value}
            points={polygon(axes.map(() => value))}
            fill="none"
            stroke="#dce0d8"
          />
        ))}
        {axes.map((axis, i) => (
          <g key={axis.id}>
            <line
              x1="180"
              y1="160"
              x2={point(i, 100)[0]}
              y2={point(i, 100)[1]}
              stroke="#dce0d8"
            />
            <text
              x={point(i, 128)[0]}
              y={point(i, 124)[1]}
              textAnchor="middle"
              dominantBaseline="middle"
            >
              {axis.label}
            </text>
          </g>
        ))}
        {baseline && (
          <polygon
            points={polygon(
              axes.map(
                (axis) =>
                  baseline.find((item) => item.id === axis.id)?.score ?? 0,
              ),
            )}
            fill="#a6b28a20"
            stroke="#889977"
            strokeDasharray="5 4"
            strokeWidth="2"
          />
        )}
        <polygon
          points={polygon(axes.map((axis) => axis.score ?? 0))}
          fill="#174b3a22"
          stroke="#174b3a"
          strokeWidth="2"
        />
        {axes.map((axis, i) => (
          <circle
            key={axis.id}
            cx={point(i, axis.score ?? 0)[0]}
            cy={point(i, axis.score ?? 0)[1]}
            r="3"
            fill="#174b3a"
          />
        ))}
        <text x="184" y="54" className="chart-scale">
          100
        </text>
        <text x="184" y="143" className="chart-scale">
          20
        </text>
      </svg>
      <div className="chart-legend">
        <span>
          <i />
          当前检测
        </span>
        {baseline && (
          <span>
            <i className="baseline" />
            历史三次均值
          </span>
        )}
      </div>
      <div className="table-wrap">
        <table>
          <caption className="sr-only">五维能力得分，满分 100</caption>
          <thead>
            <tr>
              <th>维度</th>
              <th>当前 / 100</th>
              {baseline && <th>基线 / 100</th>}
            </tr>
          </thead>
          <tbody>
            {axes.map((axis) => (
              <tr key={axis.id}>
                <td>{axis.label}</td>
                <td>
                  {axis.score === undefined ? '—' : axis.score.toFixed(1)}
                </td>
                {baseline && (
                  <td>
                    {baseline
                      .find((item) => item.id === axis.id)
                      ?.score.toFixed(1) ?? '—'}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export default function RadarPage() {
  const [groups, setGroups] = useState<RadarGroup[]>([])
  const [selected, setSelected] = useState('')
  const [model, setModel] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const [suite, setSuite] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [loaded, setLoaded] = useState(false)
  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setGroups(await intelligenceApi.radar())
      setLoaded(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : '雷达读取失败。')
    } finally {
      setLoading(false)
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])
  const candidates = groups.filter(
    (group) =>
      (!model || group.model === model) &&
      (!endpoint || group.endpoint_label === endpoint) &&
      (!suite || group.suite_version === suite),
  )
  const group = candidates.find((item) => item.id === selected) ?? candidates[0]
  const runs = useMemo(
    () =>
      [...(group?.runs ?? [])].sort(
        (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
      ),
    [group],
  )
  const latest = runs.find((run) => run.run_id === group?.latest_run_id)
  const comparable = !!latest && isComparableIntelligence(latest)
  const history = runs.filter(
    (run) => run.run_id !== latest?.run_id && isComparableIntelligence(run),
  )
  const baseline =
    history.length >= 3 && group?.baseline_score !== undefined
      ? latest?.intelligence_report?.dimensions.map((dimension) => ({
          ...dimension,
          score:
            history
              .slice(0, 3)
              .reduce(
                (sum, run) =>
                  sum +
                  (run.intelligence_report?.dimensions.find(
                    (item) => item.id === dimension.id,
                  )?.score ?? 0),
                0,
              ) / 3,
        }))
      : undefined
  const signal =
    latest && !comparable
      ? '当前检测不可比较'
      : group?.signal === 'review'
        ? '值得复核'
        : group?.signal === 'stable'
          ? '暂未触发观察阈值'
          : '历史样本不足'
  return (
    <div
      className="content intelligence-page radar-page"
      data-testid="radar-page"
    >
      <section className="intelligence-banner">
        <div>
          <span className="kicker">INTELLIGENCE OBSERVATORY / 02</span>
          <h2>让变化，有迹可循。</h2>
          <p>同一目标，同一题库。观察五维能力的分布与历史变化。</p>
        </div>
        <Radar size={70} strokeWidth={1} aria-hidden="true" />
      </section>
      <div className="section-heading">
        <div>
          <span className="eyebrow">COMPARABLE MEASUREMENTS</span>
          <h3>智商雷达</h3>
        </div>
        <button
          className="secondary small"
          disabled={loading}
          onClick={() => void load()}
        >
          <RefreshCw size={15} className={loading ? 'spin' : ''} />
          刷新数据
        </button>
      </div>
      <p className="intelligence-note">
        这是公开小题库的能力通过率，不代表人类 IQ
        或供应商身份。仅完整、无传输错误的成功检测参与比较。
      </p>
      {error && (
        <div className="alert error" role="alert">
          <CircleAlert size={17} />
          <span>
            {error}
            {loaded ? ' 当前显示上次读取的数据，可能已过期。' : ''}
          </span>
        </div>
      )}
      {loading && (
        <p role="status" className="intelligence-note">
          正在读取雷达数据…
        </p>
      )}
      {loaded && (
        <>
          <div className="radar-filters form-panel">
            <label className="field">
              <span>模型</span>
              <select
                value={model}
                onChange={(e) => {
                  setModel(e.target.value)
                  setSelected('')
                }}
              >
                <option value="">全部模型</option>
                {[...new Set(groups.map((item) => item.model))].map((value) => (
                  <option key={value}>{value}</option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Endpoint</span>
              <select
                value={endpoint}
                onChange={(e) => {
                  setEndpoint(e.target.value)
                  setSelected('')
                }}
              >
                <option value="">全部目标</option>
                {[...new Set(groups.map((item) => item.endpoint_label))].map(
                  (value) => (
                    <option key={value} value={value}>
                      {value || 'Mock 本地模拟'}
                    </option>
                  ),
                )}
              </select>
            </label>
            <label className="field">
              <span>题库</span>
              <select
                value={suite}
                onChange={(e) => {
                  setSuite(e.target.value)
                  setSelected('')
                }}
              >
                <option value="">全部题库</option>
                {[...new Set(groups.map((item) => item.suite_version))].map(
                  (value) => (
                    <option key={value}>{value}</option>
                  ),
                )}
              </select>
            </label>
            <label className="field">
              <span>可比分组 · 来源与重复次数</span>
              <select
                value={group?.id ?? ''}
                onChange={(e) => setSelected(e.target.value)}
              >
                {candidates.map((item) => (
                  <option key={item.id} value={item.id}>
                    {intelligenceSource(item.source_type)} · {item.repetitions}{' '}
                    次 · {item.model}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {!group ? (
            <div className="empty-state">
              <Radar size={28} />
              <h4>还没有可比的检测</h4>
              <p>
                完成一次智力检测后，雷达会按模型、目标、题库和重复次数分别归组。失败和取消记录可在检测历史复核。
              </p>
              <a className="secondary" href="#intelligence">
                前往智力检测 <ArrowRight size={15} />
              </a>
            </div>
          ) : (
            <>
              <div className="radar-overview">
                <section className="form-panel radar-measure">
                  <div className="panel-heading">
                    <div>
                      <span className="eyebrow">FIVE DIMENSIONS / 100</span>
                      <h3>{group.model}</h3>
                    </div>
                    <span className="mock-badge">
                      {intelligenceSource(group.source_type)}
                    </span>
                  </div>
                  <p className="intelligence-note">
                    {group.endpoint_label || 'Mock 本地模拟'} ·{' '}
                    {group.suite_version} · 每题 {group.repetitions} 次
                  </p>
                  <>
                    {comparable ? (
                      <RadarChart
                        dimensions={
                          latest?.intelligence_report?.dimensions ?? []
                        }
                        baseline={baseline}
                      />
                    ) : (
                      <div className="empty-state">
                        <Radar size={30} />
                        <h4>等待完整的能力分布</h4>
                        <p>
                          当前记录
                          {latest
                            ? `为${intelligenceStatus(latest.status)}`
                            : '尚未读取'}
                          。未完成或含传输错误的结果不参与能力比较，请查看详情或重新检测。
                        </p>
                        {latest && (
                          <a
                            className="secondary"
                            href={`#intelligence?run=${encodeURIComponent(latest.run_id)}`}
                          >
                            查看当前记录
                          </a>
                        )}
                      </div>
                    )}
                  </>
                </section>
                <section className="form-panel radar-reading">
                  <span className="eyebrow">OBSERVATION SIGNAL</span>
                  <h3>{signal}</h3>
                  <div className="radar-score">
                    {comparable
                      ? latest?.intelligence_report?.score.toFixed(1)
                      : '—'}
                    <small>/ 100</small>
                  </div>
                  <dl className="snapshot-grid">
                    <div>
                      <dt>历史基线 · 最近三次均值</dt>
                      <dd>{group.baseline_score?.toFixed(1) ?? '—'}</dd>
                    </div>
                    <div>
                      <dt>当前减基线</dt>
                      <dd>
                        {group.delta === undefined
                          ? '—'
                          : `${group.delta > 0 ? '+' : ''}${group.delta.toFixed(1)} 分`}
                      </dd>
                    </div>
                    <div>
                      <dt>当前 Run</dt>
                      <dd>{group.latest_run_id}</dd>
                    </div>
                    {group.baseline_run_id && (
                      <div>
                        <dt>基线参考 Run</dt>
                        <dd>{group.baseline_run_id}</dd>
                      </div>
                    )}
                  </dl>
                  <p className="intelligence-note">
                    {!comparable
                      ? '当前检测尚不能形成比较；历史记录保留在下方，失败、取消和未完成不影响模型能力判断。'
                      : group.signal === 'insufficient_data'
                        ? '需要当前检测之外至少 3 次完整历史，才能形成基线。'
                        : '较历史均值下降至少 15 分时提示复核；这只是观察规则，不能据此断言模型降智。'}{' '}
                    传输失败不计为模型能力退化。
                  </p>
                  {group.source_type === 'mock' && (
                    <p className="intelligence-note">
                      Mock profile 可发生变化，仅作演示对照；每条记录的 profile
                      在详情公开。
                    </p>
                  )}
                </section>
              </div>
              <section className="form-panel">
                <div className="panel-heading">
                  <div>
                    <span className="eyebrow">MEASUREMENT HISTORY</span>
                    <h3>趋势与可比历史</h3>
                  </div>
                  <span className="hint">
                    {runs.length} 条记录 ·{' '}
                    {runs.filter(isComparableIntelligence).length} 次可比
                  </span>
                </div>
                <div
                  className="radar-trend"
                  role="img"
                  aria-label="按时间排序的分数趋势，具体分数见下方历史表"
                >
                  {[...runs].reverse().map((run) => (
                    <a
                      key={run.run_id}
                      href={`#intelligence?run=${encodeURIComponent(run.run_id)}`}
                      title={`${new Date(run.created_at).toLocaleString('zh-CN')} · ${isComparableIntelligence(run) ? `${run.intelligence_report?.score.toFixed(1)} 分` : intelligenceStatus(run.status) + ' · 不可比较'}`}
                    >
                      <span>
                        {isComparableIntelligence(run)
                          ? run.intelligence_report?.score.toFixed(0)
                          : '—'}
                      </span>
                      {isComparableIntelligence(run) ? (
                        <i
                          style={{
                            height: `${Math.max(2, run.intelligence_report?.score ?? 0)}%`,
                          }}
                        />
                      ) : (
                        <i className="trend-unavailable" />
                      )}
                      <small>
                        {new Date(run.created_at).toLocaleDateString('zh-CN', {
                          month: 'numeric',
                          day: 'numeric',
                        })}
                      </small>
                    </a>
                  ))}
                </div>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>检测时间</th>
                        <th>来源 / 场景</th>
                        <th>状态</th>
                        <th>分数</th>
                        <th>完成度</th>
                        <th>记录</th>
                      </tr>
                    </thead>
                    <tbody>
                      {runs.map((run) => (
                        <tr key={run.run_id}>
                          <td>
                            {new Date(run.created_at).toLocaleString('zh-CN')}
                          </td>
                          <td>
                            {run.source_type === 'mock'
                              ? `Mock · ${run.config.intelligence.mock_profile}`
                              : '真实 API'}
                          </td>
                          <td>
                            {intelligenceStatus(run.status)}
                            {!isComparableIntelligence(run) && ' · 不可比'}
                          </td>
                          <td>
                            {isComparableIntelligence(run)
                              ? `${run.intelligence_report?.score.toFixed(1)} / 100`
                              : '—'}
                          </td>
                          <td>
                            {run.intelligence_report?.completed ??
                              run.completed_count}{' '}
                            /{' '}
                            {run.intelligence_report?.total ??
                              10 * run.config.intelligence.repetitions}
                          </td>
                          <td>
                            <a
                              href={`#intelligence?run=${encodeURIComponent(run.run_id)}`}
                            >
                              查看详情
                            </a>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            </>
          )}
        </>
      )}
    </div>
  )
}
