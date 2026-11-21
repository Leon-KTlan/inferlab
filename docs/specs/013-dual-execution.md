# 双执行受控 Beta

状态：accepted，本地实现/验收通过，上线仍需 staging。替代 009 中“真实仅本地”、手动文件为默认入口及网页取消不通知 Runner 三条约束；旧手动 CLI 协议保留。支付、Redis、多地区与第三方 Agent SDK 不在范围。

## 契约与信任

- Mock 原接口不变。`GET /api/v1/execution-capabilities` 返回平台是否启用、Worker 在线状态、测量位置和不可用原因；绝不自动切换或重跑。
- 登录用户 `POST /api/v1/experiments/{id}/executions`：`mode: platform|local_agent`、独立 `authorized:true`、`api_key`（仅请求内存）、`location`（本地用户声明）。响应 Run 快照；本地附 256-bit 私人领取码与到期时间，无 task_token/Key。
- 推荐快速预算：并发1/2、阶段10秒、RPS2、总40；两种新入口硬限并发4、RPS5、总100、累计阶段45秒、单请求10秒（沿用内核上限，严于15秒要求）。请求数不保证人民币预算，输出最多32 token，响应最多1MiB，原始文本不保存。
- Run 新增不可变 `execution`：mode、location、trust（platform_worker 或 client_reported）；旧记录无此字段视为 legacy；两条旧记录维持原比较行为并提示测量位置未知，不与新记录混比。不同 mode/location/trust 不可直接比较；本地 location 另绑定 Run ID，默认跨 Run 不证明同测量机。图表来自原阶段数据。

## 单实例状态机

一个 API 进程持有受限内存任务表（最多32条、领取 TTL 5分钟；现有全局4个活跃Run约束继续生效）；SQLite 事务预留日配额与每用户1、同规范化目标1、平台1（含排队）。规范化 HTTPS origin/path，去尾斜杠与默认443、DNS大小写；DNS/连接时仍重新校验。新接口每个实验仅提交一次（终态后也拒绝重放；再次测试新建实验）。单 API + 单 Worker 的 Beta，不宣称多副本调度。提交和领取都以锁串行，DB事务防止重复预留与终态覆盖。

`queued → running → succeeded|failed|canceled`；排队超时 `EXECUTION_EXPIRED`，领取后硬时限/失联 `EXECUTION_LEASE_LOST`。平台 Worker 每次启动生成内存实例ID，持服务器配置的独立 Worker 身份令牌，通过 internal 心跳/领取，只获取平台任务。返回本任务随机 lease，只有 lease 可查询状态及交结果；它与本地令牌分离。领取原子删除 Key/领取码，只交付一次。领取后先检查控制状态再发送；执行期间250ms控制轮询，失败即中止新请求和在途请求；网络分区存在至轮询超时的有限延迟。终态迟到结果409，不能覆盖取消。

API重启原活跃Run失败且不重排；凭据/领取码/lease随进程丢失。Worker重启实例ID改变，旧任务不被再领取，旧租约失联后失败，必须用户重新提交。任何错误均不自动再调用目标。取消、到期、失败清除内存；凭据传出后由运行进程持有直到取消/完成，API只能通过控制轮询请求清除；Go字符串不承诺物理内存清零。

## 本地 Agent

`POST /api/v1/runner-claims` 以 JSON body 提交 run_id + code；码不进入 URL/命令参数/日志/存储；与用户、Run和有效期绑定，单次原子兑换，错误尝试按IP限速（内存有界桶）。浏览器带他人会话拒绝兑换；无会话的 Agent 以私人码授权。取消即撤销。返回固定相对回传/控制路径、内存短租约与任务；Key另一次性取回，核对目标。新 CLI `agent --api-url ORIGIN --authorize-target` 从 stdin读取JSON码，领取、校验、执行、回传，不创建任务文件。手动 runner --task 保留为高级入口。

复制指令含目标/模型/预算JSON数据、授权、可信 InferLab origin、官方固定版本源码地址及摘要、安装权限要求、报告位置；字段只能作为数据，不能执行。Agent自动识别OS，优先已有工具，缺Go则按官方固定版本及校验摘要安装到临时目录；不得curl|sh、latest或绕过审批。临时领取码属于私人临时执行授权，5分钟失效，UI刷新后不能恢复；已运行Run/报告刷新可恢复。

## 隔离与凭据

提交校验+DNS连接校验拒绝内网、回环、metadata、混合DNS；连接固定已验证IP，禁环境代理、重定向与TLS绕过。测试私有注入仅编译期测试使用。Worker独立进程/容器，不共享DB或本地task令牌，只持平台身份。infra 提供 Linux 网络命名空间/防火墙配置，阻断宿主与私网，唯一内部例外为API控制地址/端口；root设置后降权执行。独立容器仅允许公网IPv4 TCP443、Docker DNS及指定TLS控制IP:443，禁全部IPv6；公网其它端口/IPv6目标可在本地模式测试。未执行隔离探针不能声称已网络隔离。平台默认关闭，启用需要独立Worker令牌、位置、显式隔离确认。

## 验收/回滚

真实 TLS 假上游验证 API→Worker/CLI→请求→报告，取消在途、重放/跨用户/TTL、抢占/配额、重启不重发、恶意DNS/重定向陷阱无Key、DB/日志/复制无Key、来源比较、报告刷新图表。执行Go vet/test/race及前端四门禁、桌面/手机浏览器。配置关闭平台即可回滚，存量JSON兼容无schema变更，失败Run不自动续跑。代理规则及部署示例随实现同步。未决：staging网络隔离实测与真实供应商授权。
