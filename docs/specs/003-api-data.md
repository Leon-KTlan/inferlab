# API 和数据规格

状态：`draft`

## API 基线

前缀为 `/api/v1`，错误结构固定为：

```json
{"error":{"code":"INVALID_EXPERIMENT_CONFIG","message":"..."}}
```

首批接口：

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/healthz` | 健康检查 |
| `POST` | `/experiments` | 创建实验模板 |
| `GET` | `/experiments/{id}` | 查询实验配置 |
| `POST` | `/experiments/{id}/runs` | 启动 Run |
| `GET` | `/runs/{id}` | 查询状态和汇总 |
| `POST` | `/runs/{id}/cancel` | 取消 Run |
| `GET` | `/runs/{id}/events` | 查询运行事件 |
| `GET` | `/runs/{id}/report` | 读取基础报告 |
| `GET` | `/reports/{id}/compare` | 比较可比 Run |
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

## 未决项

登录方案、SQLite 到 PostgreSQL 的迁移工具、SSE 与轮询的最终选择，需要在实现第一版 API 前确认并记录。
