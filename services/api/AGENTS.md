# API 和 Runner 目录规则

- 使用 Go，优先标准库和已有依赖；边界层显式处理输入校验、超时、取消和结构化错误。
- Runner 必须同时约束并发、RPS、总请求数、阶段时长和单请求超时，并支持取消。
- Run 使用不可变配置快照；模板后续编辑不能改变已运行记录。
- Mock adapter 和 OpenAI-compatible adapter 分离，真实目标默认走本地 Runner。
- 数据库迁移必须可重复执行，禁止在启动时静默破坏已有数据。
- 日志不得包含密钥、Authorization Header、原始 Prompt 或敏感响应；错误要能定位但不能泄露机密。
- 变更至少运行 `gofmt`、`go vet ./...`、`go test ./...` 和高风险场景的 `go test -race ./...`。
