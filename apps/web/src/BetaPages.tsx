import { RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from './api'

export function BetaLinks() {
  return <nav className="beta-links" aria-label="运营信息">
    <a href="/status">服务状态与支持</a><a href="/policies">条款与隐私草案</a>
  </nav>
}

export default function BetaPages({ statusPage }: { statusPage: boolean }) {
  const [attempt, setAttempt] = useState(0)
  const [sample, setSample] = useState<Awaited<ReturnType<typeof api.publicStatus>> | null>()
  useEffect(() => {
    if (!statusPage) return
    const controller = new AbortController()
    let current = true
    const timer = window.setTimeout(() => controller.abort(), 3500)
    api.publicStatus(controller.signal).then(value => { if (current) setSample(value) }).catch(() => { if (current) setSample(null) }).finally(() => window.clearTimeout(timer))
    return () => { current = false; window.clearTimeout(timer); controller.abort() }
  }, [statusPage, attempt])
  return <main className="beta-page">
    <header><a href="/">InferLab 工作台</a><BetaLinks /></header>
    {statusPage ? <>
      <h1>服务状态与支持</h1>
      <section>
        <h2>工作台与数据服务</h2>
        <p role="status">{sample === undefined ? '正在检查…' : sample === null ? '无法确认当前状态' : sample.status === 'operational' ? '当前取样正常' : '当前取样异常'}</p>
        {sample && <p>取样时间：<time dateTime={sample.checked_at}>{new Date(sample.checked_at).toLocaleString('zh-CN')}</time></p>}
        <p>此页显示一次实时健康检查，不提供历史可用率。</p>
        <button className="secondary" disabled={sample === undefined} onClick={() => { setSample(undefined); setAttempt(value => value + 1) }}><RefreshCw size={16} />重新检查</button>
      </section>
      <section><h2>支持</h2>{sample?.support_url ? <a href={sample.support_url} rel="noreferrer">联系支持</a> : <p>支持入口暂未配置。</p>}</section>
    </> : <>
      <h1>条款与隐私</h1>
      <p className="beta-draft">邀请测试草案。运营主体、联系方式、适用地区和保存期限待确认。</p>
      <section><h2>服务条款与目标授权</h2><p>仅对你拥有或取得明确许可的目标运行实验。请遵守目标所有者允许的时段、负载及预算。外部真实目标请求由你的本地 Runner 发出。报告仅反映所测配置和采样条件。</p></section>
      <section><h2>隐私与分享</h2><p>工作台保存账户、会话、实验配置、运行指标及报告。请勿在配置字段填写秘密或个人信息。临时 Runner 凭据不写入报告；公开分享链接可被持有链接的人访问，创建前请审查内容，并可在原报告撤销。</p><p>如开通 CSV 购买，核验时会向爱猹收发送账户的内部标识和权益标识。购买在爱猹收页面完成。</p></section>
      <section><h2>保留与删除</h2><p>可在工作台删除实验或运行记录。备份按运营方制定的周期管理，删除不会立即清除历史备份。账户数据、备份保留期限和请求渠道仍待运营方确认。</p></section>
    </>}
  </main>
}
