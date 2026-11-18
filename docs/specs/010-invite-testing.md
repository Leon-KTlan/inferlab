# 邀请测试、身份和报告管理

状态：accepted，实施中；本地验收后仍须 staging 验证。

## 目标和范围

沿用 Go 单体、SQLite、React，实现邀请注册、服务端会话、数据隔离、每日预留额度、终态脱敏分享和删除。local 可匿名试用；staging/production 启动 Mock 或创建本地 Runner 任务须登录。真实 API 仍只由本地 CLI 发起，任务 token 授权的领取凭据和提交结果不要求登录。

## 身份和 API

- `GET /api/v1/auth/me`：当前用户名（匿名为空）、是否要求登录、UTC 当日额度及重置时间。
- `POST /api/v1/auth/register`：`username,password,invite,claim_anonymous`；用户名规范为小写 ASCII 3–32 位，密码 12–72 字节，不截断。邀请来自 `INVITE_CODE`，未配置则关闭注册，仅作常量时间比较，不存储或回显。密码使用 bcrypt。注册及可选匿名认领、会话轮换为一个事务。
- `POST /api/v1/auth/login`：`username,password`；登录已有账户不合并匿名记录。注册与登录均在 KDF 前按来源 IP 持久化限流；登录另按规范化用户名限流，十分钟内各最多五次尝试（包括成功与无效输入）；不信任转发头。桶总数最多 4096，满时拒绝新桶，每次有限清理过期桶和会话。登录成功轮换会话并撤销旧会话。
- `POST /api/v1/auth/logout`：撤销当前会话，后续请求获得新的匿名身份。
- 会话 token 为 192 位随机值，数据库只存 SHA-256；HttpOnly、SameSite=Strict、secure 环境 Secure cookie，绝对有效期七天。旧签名匿名 cookie 首次访问可迁移为有期限服务端会话；已撤销或已认领的旧身份不可再次迁入。
- 无 cookie 的身份写请求仅在凭据验证成功后创建账户会话，失败/429 不新建匿名会话；任务 token 请求不创建匿名会话。匿名会话最多4096、总会话最多8192，给登录用户保留容量；每次插入事务先清理最多128条过期/已撤销会话，满额503。认证限流尝试另可清理最多512条过期记录。既有有效会话不受容量上限影响。
- 写请求拒绝异源 Origin、跨站 Fetch Metadata；所有身份写接口强制 application/json（包括退出），JSON 输入保持严格解析。无 Origin 的 CLI 保持兼容，浏览器同源 JSON 请求受 SameSite 和 Origin 双重约束。

## 数据库 v2

新增 `users(id,username,password_hash)`、`sessions(token_hash,owner,user_id,expires_at,revoked)`、`anonymous_claims(owner,user_id)`、`login_attempts(bucket,window_start,failures)`、`daily_quotas(owner,day,reserved)`、`report_shares(token,run_id,owner,created_at)`。保留 v1 原表与数据；每次迁移与 user_version 更新同一事务，可重复启动。

认领仅在注册时执行一次；原子更新 experiments/runs 的 owner、合并各 UTC 日额度、记录已认领身份并撤销该匿名身份所有会话。并发旧请求不得在认领后新建遗留记录。Runner 写入依 Run ID，取消时使用当前数据库归属；任务 token 与 owner 解耦，认领不改变任务 token。

## 配额

`DAILY_REQUEST_LIMIT` 默认 10000，正整数。每个 owner、UTC 日预留 `max_requests`；Mock 和本地任务共用创建 Run 的事务，检查已有活跃任务、配额后插入 Run。失败全部回滚；启动后完成、失败、取消、删除均不退额度。认领合并额度，即使总额超过配置也不截断。`429 DAILY_REQUEST_QUOTA_EXCEEDED` 返回 `limit,reserved,remaining,reset_at`。这只是平台创建任务的预留额度，不能约束用户额外在本地发起的请求，也不是供应商账单。

## 分享和删除

- `POST /api/v1/runs/{id}/share`：仅本人终态 Run，返回随机 192 位 token 和 `/share/{token}` 路径；一个 Run 一条可重复获取的分享。
- `DELETE /api/v1/runs/{id}/share`：撤销分享，持久化；再次公开读取为 404。
- `GET /api/v1/shares/{token}`：无需账户；显式 DTO 白名单，仅状态、来源/结果类型、总体和阶段数值指标、SLO 配置/判定；不含任何内部 ID、时间、名称、模型、endpoint、owner、事件、任务、凭据或诊断。公共页面只请求此端点。
- `DELETE /api/v1/runs/{id}`、`DELETE /api/v1/experiments/{id}`：仅本人；存在 queued/running 返回 409。事务删除关联分享、samples、events、Run，实验删除还删除实验本身。跨 owner 读写均为 404。

## 验收和发布

新增 API 集成测试覆盖身份、v1 迁移、认领竞态、持久化限流/额度、并发预留、UTC 换日、失败回滚、分享哨兵、跨 owner 和活跃删除。新增真实浏览器邀请闭环；保留所有旧断言与 smoke。执行 Go vet/test/race、前端 lint/check、旧 UI smoke。独立临时数据库与端口，禁止调用真实供应商。

先备份 SQLite（停止写入或 SQLite backup），迁移自动执行。回滚须同时恢复 v1 备份及旧二进制；旧二进制拒绝 v2，禁止直接降低 user_version。staging 凭据、部署证据、支付和监控接入为外部依赖。本批仅 PR，不合并或生产发布。

应用限流只使用实际连接来源，不信任 X-Forwarded-For 等转发头。反向代理后多个用户可能共享 IP 桶；staging 必须验证代理拓扑、账户登录和注册限流体验，后续如需真实来源须明确可信代理边界，不能直接信任任意转发头。代理须保留原 Host 以供同源 Origin 校验。
