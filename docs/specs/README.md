# InferLab 技术规格索引

这些文档是实现和发布的共同依据。状态说明：`accepted` 表示当前执行标准，`draft` 表示仍需架构负责人确认，`superseded` 表示被新版本替代。

| 文档 | 主题 | 状态 |
|---|---|---|
| `001-project-charter.md` | 产品边界和完成标准 | accepted |
| `002-architecture.md` | 系统架构和演进 | accepted |
| `003-api-data.md` | API、数据模型和版本化 | accepted |
| `004-security-operations.md` | 安全、阿里云和运维 | accepted |
| `005-testing-delivery.md` | 测试、CI/CD 和发布 | accepted |
| `006-collaboration.md` | AI 协作、Git 和 token 控制 | accepted |
| `007-staging-runbook.md` | staging、备份、回滚和发布后检查 | accepted |
| `008-autonomous-execution.md` | 无人值守执行、模型选择和交接记录 | accepted |
| `009-local-runner-cli.md` | 本地 Runner CLI、网页交接和安全验收 | accepted |
| `010-invite-testing.md` | 邀请身份、迁移、每日预留额度、脱敏分享和删除 | accepted；本地实现，待 staging |
| `011-payments-monitoring-plan.md` | 后续支付、监控和真实外部依赖 | draft；未实现 |

决策规则：实现遇到规格冲突时先停止扩大改动，根代理更新规格中的决策记录，再继续实现。
