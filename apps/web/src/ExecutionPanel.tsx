import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { buildAgentInstructions, type AgentHandoff, type ExecutionMode } from './agent-handoff'
import type { ExecutionCapabilities } from './api'

export function ExecutionChoice({ mode, setMode, capabilities, onRefresh }: { mode: ExecutionMode; setMode: (mode: ExecutionMode) => void; capabilities?: ExecutionCapabilities; onRefresh: () => void }) {
  return <section className="form-panel" aria-label="执行方式"><h3>执行方式</h3><div className="segmented"><button type="button" className={mode === 'platform' ? 'active' : ''} onClick={() => setMode('platform')}>平台代跑 · 推荐</button><button type="button" className={mode === 'local_agent' ? 'active' : ''} onClick={() => setMode('local_agent')}>本地 Agent · 可选</button></div><p>{mode === 'platform' ? capabilities?.platform_available ? `测量位置：${capabilities.location}；由独立 Worker 执行。` : capabilities?.reason ?? '正在读取平台能力；暂不可运行。' : '复制完整任务给本地 Agent，由它准备工具、执行和回传。测量位置为你的电脑，结果属于客户端上报。'}</p>{mode === 'platform' && <button className="text-button" type="button" onClick={onRefresh}>刷新平台状态</button>}<p className="hint">最多40请求的快速体检不等于精确金额预算；请确认下方实际预算。取消会在控制轮询后中止请求，网络故障也会停止。</p></section>
}

export function AgentHandoffPanel({ task }: { task?: AgentHandoff }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState('')
  const expired = task ? Date.parse(task.expires_at) <= Date.now() : false
  const copy = async () => { if (!task || expired) return; try { await navigator.clipboard.writeText(buildAgentInstructions(window.location.origin, task)); setCopied(true); setError('') } catch { setError('复制失败，请检查浏览器剪贴板权限。') } }
  return <section className="events-panel runner-handoff" aria-label="本地 Agent 交接"><h3>{task ? '交给本地 Agent 执行' : '领取码已从页面清除'}</h3>{task ? <><p>一键复制给你信任的本地 Agent。它会自动准备固定版本工具、领取本次任务并回传报告，无需手动克隆或移动任务文件。</p><p className="handoff-copy">复制内容含私人临时执行授权，仅本次 Run、单次领取，5分钟失效。不要公开或转发。API Key 不在复制内容中；刷新会清除领取码。</p><button className="secondary" disabled={expired} onClick={copy}>{copied ? <Check size={16} /> : <Copy size={16} />}{expired ? '领取授权已过期，请取消后新建' : copied ? '已复制完整任务' : '一键复制给本地 Agent'}</button></> : <p>已启动的 Agent 可继续回传。尚未启动则取消旧任务后重新创建；领取码不会从浏览器存储恢复。</p>}{error && <p role="alert">{error}</p>}<details><summary>高级手动 CLI</summary><p>保留原 runner --task 协议。已安装本次固定版本工具的用户也可直接使用 agent --api-url 加可信 InferLab origin 和 --authorize-target，由进程 stdin 提供私人领取 JSON。不要将凭据放入命令参数。</p></details></section>
}
