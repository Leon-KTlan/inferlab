# API 和数据规格

状态：`accepted`

本规格区分当前可运行的 Mock MVP 契约和后续扩展接口。未标记为已实现的接口不得在产品页面、演示或发布说明中宣称可用。

## API 基线

前缀为 `/api/v1`，错误结构固定为：

```json
{"error":{"code":"INVALID_EXPERIMENT_CONFIG","message":"..."}}
```

当前已实现接口：

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/healthz` | 健康检查 |
| `POST` | `/experiments` | 创建实验模板 |
| `GET` | `/experiments/{id}` | 查询实验配置 |
| `POST` | `/experiments/{id}/runs` | 启动 Run |
| `GET` | `/runs` | 查询当前访问者最近 Run |
| `GET` | `/runs/{id}` | 查询状态和汇总 |
| `POST` | `/runs/{id}/cancel` | 取消 Run |
| `GET` | `/runs/{id}/events` | 查询运行事件 |
| `GET` | `/runs/{id}/report` | 读取基础报告 |
| `GET` | `/reports/{id}/compare` | 比较可比 Run |

后续扩展接口（当前未实现）：

| `POST` | `/orders` | 创建增值报告订单 |
| `POST` | `/payments/notify` | 接收服务端支付通知 |
| `GET` | `/reports/{id}/premium` | 校验权益后读取增值报告 |

## 核心状态

Run：`queued`、`running`、`succeeded`、`failed`、`canceled`。

订单：`created`、`pending`、`paid`、`fulfilled`，以及 `failed`、`closed`、`refunded`。

## 数据约束

- Run 必须保存实验配置快照、来源类型、软件版本、采样窗口、错误码和汇总指标。
- 订单金额用最小货币单位整数保存，权益独立建模并通过订单幂等授予。
- 不保存明文 Key、Authorization Header、原始 Prompt 或敏感响应。
- 数据库迁移必须版本化，外键、唯一约束和索引随实现补齐。

## v0.1 决策记录

- **访问身份**：v0.1 使用 HttpOnly、SameSite=Strict 的签名匿名 Cookie 识别访问者；不提供账号、找回会话或跨访问者共享。所有实验、Run 和报告查询都按访问者隔离。
- **持久化**：v0.1 使用单实例 SQLite，schema 通过 `PRAGMA user_version` 版本化迁移，启用 WAL 和外键约束。SQLite 到 PostgreSQL 的迁移工具留到多实例或团队功能阶段，不阻塞当前 MVP。
- **状态读取**：v0.1 使用 `GET /api/v1/runs/{id}` 轮询状态，并使用带游标的 `/api/v1/runs/{id}/events?after=` 读取事件；SSE 等推送协议留到连接规模和部署模型明确后再评估。
- **支付和权益**：订单、权益及支付宝/VibePay 适配器只在服务端验签、查单和幂等规则实现后启用；当前仓库不提供支付接口，也不产生真实订单。
- **真实目标**：当前 Runner 只实现 Mock 目标。OpenAI 兼容目标、本地 Runner 和出站网络安全门禁需单独规格和测试证据后再开放。
