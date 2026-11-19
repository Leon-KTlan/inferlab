import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { CircleAlert, LoaderCircle, LogIn, LogOut, UserRound, X } from 'lucide-react'
import { api, type Identity } from './api'
import { accountErrorMessage, validateAccount, validateRegistration } from './registration'
export { validateAccount } from './registration'

export default function AccountPanel({ identity, error, retry }: { identity?: Identity; error: string; retry: () => void }) {
  const [mode, setMode] = useState<'login' | 'register'>()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [invite, setInvite] = useState('')
  const [claim, setClaim] = useState(true)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const dialogRef = useRef<HTMLElement>(null)
  const busyRef = useRef(false)
  const isOpen = mode !== undefined
  const registrationOpen = identity?.registration_open === true
  const clearSensitive = () => { setPassword(''); setConfirmation(''); setInvite('') }
  const changeMode = (next: 'login' | 'register') => { if (busyRef.current) return; clearSensitive(); setActionError(''); setMode(next) }
  const close = () => { if (busyRef.current) return; clearSensitive(); setActionError(''); setMode(undefined) }
  useEffect(() => {
    if (!isOpen) return
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
    const background = document.querySelector<HTMLElement>('.app-shell')
    const previousInert = background?.inert ?? false
    const previousOverflow = document.body.style.overflow
    if (background) background.inert = true
    document.body.style.overflow = 'hidden'
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        if (!busyRef.current) { setPassword(''); setConfirmation(''); setInvite(''); setActionError(''); setMode(undefined) }
      } else if (event.key === 'Tab') {
        const elements = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), a[href]') ?? [])
        const first = elements[0], last = elements[elements.length - 1]
        if (!first) { event.preventDefault(); dialogRef.current?.focus(); return }
        if (event.shiftKey && (document.activeElement === first || !dialogRef.current?.contains(document.activeElement))) { event.preventDefault(); last.focus() }
        else if (!event.shiftKey && (document.activeElement === last || !dialogRef.current?.contains(document.activeElement))) { event.preventDefault(); first.focus() }
      }
    }
    document.addEventListener('keydown', keyboard, true)
    return () => {
      document.removeEventListener('keydown', keyboard, true)
      if (background) background.inert = previousInert
      document.body.style.overflow = previousOverflow
      if (opener?.isConnected) opener.focus()
    }
  }, [isOpen])
  useEffect(() => { if (mode) (dialogRef.current?.querySelector<HTMLElement>('input[autocomplete="username"]') ?? dialogRef.current?.querySelector<HTMLElement>('button[aria-label="关闭账户窗口"]'))?.focus() }, [mode, registrationOpen])
  const resetWorkspace = () => { window.localStorage.removeItem('inferlab:last-run'); window.location.assign('/') }
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (busyRef.current || !mode || (mode === 'register' && !registrationOpen)) return
    const invalid = mode === 'register' ? validateRegistration(username, password, confirmation, invite) : validateAccount(username, password)
    if (invalid) { setActionError(invalid); clearSensitive(); return }
    busyRef.current = true; setBusy(true); setActionError('')
    try {
      if (mode === 'register') await api.register({ username, password, invite, claim_anonymous: claim })
      else await api.login({ username, password })
      clearSensitive(); resetWorkspace()
    } catch (e) { setActionError(accountErrorMessage(e)); clearSensitive(); busyRef.current = false; setBusy(false) }
  }
  const logout = async () => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true); setActionError('')
    try { await api.logout(); resetWorkspace() }
    catch (e) { setActionError(accountErrorMessage(e)); clearSensitive(); busyRef.current = false; setBusy(false) }
  }
  return <section className="account-band" aria-label="账户与额度">
    <div className="account-status"><UserRound size={18} /><strong>{identity ? identity.username || '匿名试用' : error ? '账户读取失败' : '正在读取账户…'}</strong>
      {identity?.authenticated ? <button className="text-button" onClick={logout} disabled={busy}><LogOut size={15} />退出</button> : <div className="account-actions"><button className="text-button" disabled={!identity || busy} onClick={() => changeMode('login')}><LogIn size={15} />登录</button><button className="text-button" disabled={!identity || busy} onClick={() => changeMode('register')}>邀请注册</button>{identity && !registrationOpen && <span className="registration-status">注册暂未开放</span>}</div>}
      {error && <button className="text-button" onClick={retry}>重试</button>}
    </div>
    {identity && <div className="quota-line"><span>今日预留请求 <strong>{identity.quota.reserved} / {identity.quota.limit}</strong></span><span>剩余 {identity.quota.remaining} · UTC 00:00 重置</span></div>}
    <p className="account-note">额度按任务最大请求数预留；启动后取消或删除均不退还，不代表供应商账单。额外本地请求不受平台额度控制。{identity?.login_required && !identity.authenticated ? '启动实验前请登录。' : ''}</p>
    {error && <p role="alert">{error}</p>}
    {actionError && !mode && <p role="alert">{actionError}</p>}
    {mode && createPortal(<div className="auth-dialog-backdrop"><section ref={dialogRef} tabIndex={-1} className="auth-dialog" role="dialog" aria-modal="true" aria-labelledby="auth-heading" aria-describedby="auth-description" aria-busy={busy}><div className="panel-heading"><h2 id="auth-heading">{mode === 'register' ? registrationOpen ? '邀请注册' : '注册暂未开放' : '登录账户'}</h2><button aria-label="关闭账户窗口" title="关闭" disabled={busy} onClick={close}><X size={18} /></button></div>
      <div className="segmented auth-modes" aria-label="账户操作"><button type="button" aria-label="切换到登录" aria-pressed={mode === 'login'} className={mode === 'login' ? 'active' : ''} disabled={busy} onClick={() => changeMode('login')}>登录</button><button type="button" aria-label="切换到邀请注册" aria-pressed={mode === 'register'} className={mode === 'register' ? 'active' : ''} disabled={busy} onClick={() => changeMode('register')}>邀请注册</button></div>
      {mode === 'register' && !registrationOpen ? <div className="registration-closed"><p id="auth-description">本站暂不接受新账户注册。已有账户仍可登录；持有邀请码也需等待注册重新开放。</p><button className="primary" onClick={() => changeMode('login')}><LogIn size={16} />前往登录</button></div> : <form onSubmit={submit}><fieldset disabled={busy} className="auth-fields"><label className="field"><span>用户名</span><input required autoComplete="username" autoCapitalize="none" spellCheck={false} maxLength={32} value={username} onChange={e => setUsername(e.target.value)} /></label><label className="field"><span>密码</span><input required type="password" autoComplete={mode === 'register' ? 'new-password' : 'current-password'} aria-describedby={mode === 'register' ? 'auth-password-hint' : undefined} value={password} onChange={e => setPassword(e.target.value)} /></label>
        {mode === 'register' ? <><p id="auth-password-hint">12–72 UTF-8 字节，中文字符通常占 3 字节。</p><label className="field"><span>确认密码</span><input required type="password" autoComplete="new-password" value={confirmation} onChange={e => setConfirmation(e.target.value)} /></label><label className="field"><span>邀请码</span><input required type="password" autoComplete="off" value={invite} onChange={e => setInvite(e.target.value)} /></label><label className="checkbox"><input type="checkbox" checked={claim} onChange={e => setClaim(e.target.checked)} />将当前匿名实验和预留额度归入新账户</label><p id="auth-description">注册后自动登录。未认领的匿名数据会保留在原身份中，注册后无法通过新账户访问。</p></> : <p id="auth-description">登录现有账户不会合并匿名实验；原匿名数据保留在原身份中，登录后该匿名会话失效。请先保存需要的报告。</p>}
        <button className="primary" type="submit" disabled={busy}>{busy ? <LoaderCircle className="spin" size={16} /> : <LogIn size={16} />}{busy ? '处理中…' : mode === 'register' ? '注册并登录' : '登录'}</button></fieldset>{actionError && <div className="alert error" role="alert"><CircleAlert size={16} /><span>{actionError}</span></div>}
      </form>}</section></div>, document.body)}
  </section>
}
