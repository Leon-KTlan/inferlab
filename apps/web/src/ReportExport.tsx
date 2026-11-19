import { Download, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { api } from './api'

export default function ReportExport({ runId }: { runId: string }) {
  const [access, setAccess] = useState<Awaited<ReturnType<typeof api.reportExportAccess>>>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const check = async () => {
    setBusy(true)
    setError('')
    setAccess(undefined)
    try { setAccess(await api.reportExportAccess(runId)) }
    catch { setError('暂时无法核验导出权限，请稍后重试。') }
    finally { setBusy(false) }
  }
  const download = async () => {
    setBusy(true)
    setError('')
    try { await api.downloadReportCSV(runId) }
    catch { setAccess(undefined); setError('下载失败，请重新检查购买状态。') }
    finally { setBusy(false) }
  }
  return <section className="beta-export" aria-label="报告 CSV 导出">
    <h3>报告 CSV 导出</h3>
    <p>一次购买，永久解锁本人报告的 CSV 导出。</p>
    <div className="beta-links">
      <button className="secondary" onClick={check} disabled={busy}><RefreshCw size={16} />{busy ? '处理中…' : '检查购买状态'}</button>
      {access?.access === 'purchase_required' && access.purchase_url && <a href={access.purchase_url} target="_blank" rel="noopener noreferrer">前往爱猹收购买（新窗口）</a>}
      {access?.access === 'granted' && <button className="primary" onClick={download} disabled={busy}><Download size={16} />下载 CSV</button>}
    </div>
    <div role="status">{access?.access === 'purchase_required' ? (access.purchase_url ? '尚未解锁。购买后请在这里重新检查。' : '尚未解锁，购买入口暂不可用。') : access?.access === 'granted' ? '已解锁' : ''}</div>
    {error && <p role="alert">{error}</p>}
  </section>
}
