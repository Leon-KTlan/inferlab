# Evidence

日期：2026-11-19 UTC。基线 bf7cb90，依赖 PR #6；分支 codex/feature/registration-settings，PR base 为 codex/feature/registration-flow。

## 自动验证

- 红：`cd services/api && go test ./... -run 'TestRegistration(Setup|Settings)' -count=1`，新接口缺失时返回 404，预期测试失败。
- 绿：`go test ./... -run 'TestRegistration' -count=1`，10 个测试通过，含来源/令牌/默认 local/IPv6、并发一次性、重复用户名不提权、bcrypt 字节界限和第六次限流、失败事务回滚、v2 数据保留、RBAC、旧码失效及配置优先级。
- `gofmt -w api.go auth.go store.go registration_settings.go registration_settings_test.go registration_settings_security_test.go`：仅格式化本批 Go 文件。
- `cd services/api && go vet ./... && go test ./... && go test -race ./...`：全部通过，普通测试 10.962s，race 34.147s。
- `cd apps/web && npm run lint && npm run check`：typecheck/build/14 tests 全通过。管理员状态补充后再次构建通过；最终字节校验边界补充后运行对应测试。
- `python3 scripts/registration_settings_smoke.py`：最终管理员 UI 状态断言版通过；从空站点开始，实 API 创建管理员、保存配置、访客注册、403、换码、关闭、重启、重新登录、非 local 令牌入口；检查 URL/浏览器存储/数据库/日志无明文凭据。保存中关闭/Esc/重复提交受限、失败清空、键盘焦点和客户端确认校验通过。
- `python3 scripts/stage3_smoke.py`：完整旧邀请链路、延迟 identity、旧注册错误和键盘/窄屏断言全部通过。
- `python3 scripts/stage3_smoke.py --legacy`：原 ui_smoke.py 原样、隔离转发全部通过。
- `git diff --check`：通过。

旧脚本和已有断言未改；新 smoke 开发时发现管理员账户栏缺关闭状态，按根验收意见补状态标记并保留 UI 断言。所有自动测试使用临时 DB、随机端口，无真实供应商请求，不接触既有服务数据库。

## 图像与独立验收

本批截图：`/tmp/inferlab-registration-settings-evidence/`，包含 setup-390/320、settings-1440/390/320、old-invite-error-320、token-setup-mobile；已查看桌面设置、320 设置及390初始化，无横向溢出、文字重叠；密码和邀请码保持遮盖或清空，不记录 trace。

根聊天报告独立 go vet/full race、TestRegistration*、npm lint/check（14 tests）通过；独立真实浏览器通过 fresh init → admin save → visitor signup → 普通用户无设置入口/API 403，并检查 320/390/1440 截图。根证据目录：`/var/folders/64/br1t02qn4x1d2j0_3f53z9qr0000gn/T/inferlab-settings-root-plhex1w8`。

根聊天独占追加 CI 门禁，执行聊天仅统一提交其改动。根已 SQLite backup 并更新本地 4184，保留 DB/runtime 配置并健康检查通过，尚无管理员；备份：`/tmp/inferlab-invite-preview-DDTDMG/inferlab-pre-v3-20261119T034752Z.db`。

## 限制

没有执行云端合并或部署，没有 staging 证据。远程浏览器令牌入口使用非 local HTTP 测试环境，secure staging/production 的令牌强制和 Cookie 属性另由 Go API 测试覆盖；实际 HTTPS 代理拓扑、共享 IP 限流和回滚仍需 staging 验证。最新 HEAD CI 以 PR checks 为准，避免为纯文档反复推送。
