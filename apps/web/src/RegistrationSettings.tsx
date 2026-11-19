import { useEffect, useRef, useState } from 'react'
import { Check, Copy, Eye, EyeOff, LoaderCircle, RefreshCw, Save, Shuffle } from 'lucide-react'
import { api, type RegistrationSettings as Settings } from './api'
import { accountErrorMessage, validateNewInvite } from './registration'

export default function RegistrationSettings({ onSaved, onBusy }: { onSaved: () => void; onBusy: (busy: boolean) => void }) {
  const [settings, setSettings] = useState<Settings>()
  const [enabled, setEnabled] = useState(false)
  const [invite, setInvite] = useState('')
  const [visible, setVisible] = useState(false)
  const [copied, setCopied] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const busy = useRef(false)
  const load = async () => {
    setLoading(true); setError('')
    try { const result = await api.registrationSettings(); setSettings(result); setEnabled(result.enabled) }
    catch (e) { setError(accountErrorMessage(e)) }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])
  const clearSecret = () => { setInvite(''); setVisible(false); setCopied(false) }
  const generate = () => {
    const bytes = crypto.getRandomValues(new Uint8Array(24))
    setInvite(Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')); setVisible(false); setCopied(false); setSaved(false)
  }
  const copy = async () => {
    try { await navigator.clipboard.writeText(invite); setCopied(true) }
    catch { setError('无法写入剪贴板，请使用显隐按钮查看后复制。') }
  }
  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    if (busy.current || !settings) return
    const invalid = invite ? validateNewInvite(invite) : enabled && !settings.invite_configured ? '请先设置邀请码，再开放注册。' : ''
    if (invalid) { setError(invalid); clearSecret(); return }
    busy.current = true; setSaving(true); onBusy(true); setError(''); setSaved(false)
    try {
      const result = await api.saveRegistrationSettings({ enabled, invite })
      setSettings(result); setEnabled(result.enabled); setSaved(true); onSaved()
    } catch (e) { setError(accountErrorMessage(e)) }
    finally { clearSecret(); busy.current = false; setSaving(false); onBusy(false) }
  }
  return <div className="registration-settings">
    <h3>注册管理</h3>
    {loading ? <p role="status"><LoaderCircle className="spin" size={16} />正在读取设置…</p> : !settings ? <><p role="alert">{error}</p><button className="text-button" onClick={() => void load()}><RefreshCw size={16} />重试</button></> : <form onSubmit={save}>
      <fieldset className="auth-fields" disabled={saving}>
        <label className="checkbox"><input type="checkbox" checked={enabled} onChange={e => { setEnabled(e.target.checked); setSaved(false) }} />开放邀请注册</label>
        <p>邀请码：{settings.invite_configured ? '已配置' : '未配置'} · 来源：{settings.source === 'database' ? '数据库设置' : '部署配置'}</p>
        <p>保存后以数据库设置为准。关闭注册保留邀请码；更换后旧码立即失效，已有账户不受影响。</p>
        <label className="field"><span>新邀请码</span><span className="invite-input"><input type={visible ? 'text' : 'password'} autoComplete="off" aria-describedby="new-invite-hint" value={invite} onChange={e => { setInvite(e.target.value); setCopied(false); setSaved(false) }} /><button type="button" aria-label={visible ? '隐藏新邀请码' : '显示新邀请码'} title={visible ? '隐藏新邀请码' : '显示新邀请码'} onClick={() => setVisible(!visible)}>{visible ? <EyeOff size={17} /> : <Eye size={17} />}</button></span></label>
        <p id="new-invite-hint">8–256 UTF-8 字节，不含控制字符。留空保留当前邀请码。请先复制保存，提交后不再显示。</p>
        <div className="invite-tools"><button type="button" aria-label="随机生成邀请码" title="随机生成邀请码" onClick={generate}><Shuffle size={17} /></button><button type="button" aria-label="复制新邀请码" title="复制新邀请码" disabled={!invite} onClick={() => void copy()}>{copied ? <Check size={17} /> : <Copy size={17} />}</button>{copied && <span role="status">已复制</span>}</div>
        <button type="submit" className="primary" disabled={saving}>{saving ? <LoaderCircle size={16} className="spin" /> : <Save size={16} />}{saving ? '正在保存…' : '保存设置'}</button>
      </fieldset>
      {error && <p role="alert">{error}</p>}{saved && <p role="status">设置已保存。</p>}
    </form>}
  </div>
}
