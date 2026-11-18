import { useEffect, useState } from 'react'
import { Copy, Link, LoaderCircle, Unlink } from 'lucide-react'
import { api } from './api'

export default function ReportSharing({ runId }: { runId: string }) {
  const [path, setPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  useEffect(() => { setPath(''); setMessage('') }, [runId])
  const share = async () => {
    setBusy(true); setMessage('')
    try { setPath(new URL((await api.share(runId)).path, window.location.origin).href) }
    catch (e) { setMessage(e instanceof Error ? e.message : '分享失败。') }
    finally { setBusy(false) }
  }
  const copy = async () => { try { await navigator.clipboard.writeText(path); setMessage('链接已复制。') } catch { setMessage('无法访问剪贴板，可选择并复制下方链接。') } }
  const revoke = async () => {
    setBusy(true); setMessage('')
    try { await api.revokeShare(runId); setPath(''); setMessage('分享已撤销，旧链接已失效。') }
    catch (e) { setMessage(e instanceof Error ? e.message : '撤销失败。') }
    finally { setBusy(false) }
  }
  return <section className="sharing-band" aria-label="报告分享"><div className="share-actions"><button className="secondary" disabled={busy} onClick={share}>{busy ? <LoaderCircle className="spin" size={16} /> : <Link size={16} />}{path ? '获取分享链接' : '分享脱敏报告'}</button><button className="text-button" disabled={busy} onClick={revoke}><Unlink size={16} />撤销分享</button></div><p>公开链接仅含数值指标及 SLO，不含实验名称、模型、地址或错误诊断。持有链接的人均可查看，直至撤销。</p>{path && <div className="share-link"><input aria-label="报告分享链接" readOnly value={path} /><button title="复制分享链接" aria-label="复制分享链接" onClick={copy}><Copy size={17} /></button></div>}{message && <p role="status">{message}</p>}</section>
}
