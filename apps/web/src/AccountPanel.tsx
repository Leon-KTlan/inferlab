import { useState } from 'react'
import { CircleAlert, LoaderCircle, LogIn, LogOut, UserRound, X } from 'lucide-react'
import { api, type Identity } from './api'

export function validateAccount(username: string, password: string): string {
  if (!/^[a-z0-9_]{3,32}$/.test(username.trim().toLowerCase())) return '用户名须为 3–32 位英文字母、数字或下划线。'
  const bytes = new TextEncoder().encode(password).length
  if (bytes < 12 || bytes > 72) return '密码须为 12–72 字节，中文字符按 UTF-8 字节计算。'
  return ''
}

export default function AccountPanel({ identity, error, retry }: { identity?: Identity; error: string; retry: () => void }) {
  const [mode, setMode] = useState<'login' | 'register'>()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [invite, setInvite] = useState('')
  const [claim, setClaim] = useState(true)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const resetWorkspace = () => { window.localStorage.removeItem('inferlab:last-run'); window.location.assign('/') }
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const invalid = validateAccount(username, password)
    if (invalid || (mode === 'register' && !invite)) { setActionError(invalid || '请填写邀请码。'); return }
    setBusy(true); setActionError('')
    try {
      if (mode === 'register') await api.register({ username, password, invite, claim_anonymous: claim })
      else await api.login({ username, password })
      setPassword(''); setInvite(''); resetWorkspace()
    } catch (e) { setActionError(e instanceof Error ? e.message : '身份验证失败。'); setPassword(''); setInvite(''); setBusy(false) }
  }
  const logout = async () => {
    setBusy(true); setActionError('')
    try { await api.logout(); resetWorkspace() }
    catch (e) { setActionError(e instanceof Error ? e.message : '退出失败。'); setBusy(false) }
  }
  return <section className="account-band" aria-label="账户与额度">
    <div className="account-status"><UserRound size={18} /><strong>{identity ? identity.username || '匿名试用' : error ? '账户读取失败' : '正在读取账户…'}</strong>
      {identity?.authenticated ? <button className="text-button" onClick={logout} disabled={busy}><LogOut size={15} />退出</button> : identity && <div className="account-actions"><button className="text-button" onClick={() => { setMode('login'); setActionError('') }}><LogIn size={15} />登录</button>{identity.registration_open && <button className="text-button" onClick={() => { setMode('register'); setActionError('') }}>邀请注册</button>}</div>}
      {error && <button className="text-button" onClick={retry}>重试</button>}
    </div>
    {identity && <div className="quota-line"><span>今日预留请求 <strong>{identity.quota.reserved} / {identity.quota.limit}</strong></span><span>剩余 {identity.quota.remaining} · UTC 00:00 重置</span></div>}
    <p className="account-note">额度按任务最大请求数预留；启动后取消或删除均不退还，不代表供应商账单。额外本地请求不受平台额度控制。{identity?.login_required && !identity.authenticated ? '启动实验前请登录。' : ''}</p>
    {error && <p role="alert">{error}</p>}
    {actionError && !mode && <p role="alert">{actionError}</p>}
    {mode && <div className="auth-dialog-backdrop"><section className="auth-dialog" role="dialog" aria-modal="true" aria-labelledby="auth-heading"><div className="panel-heading"><h2 id="auth-heading">{mode === 'register' ? '邀请注册' : '登录账户'}</h2><button aria-label="关闭账户窗口" title="关闭" disabled={busy} onClick={() => { setMode(undefined); setPassword(''); setInvite(''); setActionError('') }}><X size={18} /></button></div>
      <form onSubmit={submit}><label className="field"><span>用户名</span><input required autoFocus autoComplete="username" maxLength={32} value={username} onChange={e => setUsername(e.target.value)} /></label><label className="field"><span>密码</span><input required type="password" autoComplete={mode === 'register' ? 'new-password' : 'current-password'} value={password} onChange={e => setPassword(e.target.value)} /></label>
        {mode === 'register' ? <><label className="field"><span>邀请码</span><input required type="password" autoComplete="off" value={invite} onChange={e => setInvite(e.target.value)} /></label><label className="checkbox"><input type="checkbox" checked={claim} onChange={e => setClaim(e.target.checked)} />将当前匿名实验和预留额度归入新账户</label><p>密码长度为 12–72 字节。未认领的匿名数据会保留在原身份中，注册后无法通过新账户访问。</p></> : <p>登录现有账户不会合并匿名实验；原匿名数据保留在原身份中，登录后该匿名会话失效。请先保存需要的报告。</p>}
        {actionError && <div className="alert error" role="alert"><CircleAlert size={16} />{actionError}</div>}<button className="primary" type="submit" disabled={busy}>{busy ? <LoaderCircle className="spin" size={16} /> : <LogIn size={16} />}{busy ? '处理中…' : mode === 'register' ? '注册并登录' : '登录'}</button>
      </form></section></div>}
  </section>
}
