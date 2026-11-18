# 邀请测试本地验收证据

基线 `0b8fca6`，工作分支 `codex/feature/invite-test`，目标 base `codex/feature/openai-compatible-runner`（依赖 #4）。实现代理独占业务代码；根代理另负责CI最小门禁。规则、原测试断言、原 UI smoke、部署配置未改。未读取旧数据库，未停止原8080/4173服务，未调用真实供应商。

## 行为与文件

- `services/api/auth.go`：bcrypt、用户名/密码校验、有期限服务端会话、撤销/轮换、同源/JSON约束、KDF前有界持久化限流、匿名认领。无cookie的失败认证不写会话；匿名4096/总8192会话容量与分批清理。
- `services/api/store.go`、`invite_store.go`、`api.go`、`runner.go`、`sharing.go`：v1→v2事务迁移、保留活跃归属、原子预留与回滚、终态公开DTO、事务撤销和关联删除。任务token端点保持可用，SSRF/临时凭据/结果校验逻辑保留。
- 新增 `invite*_test.go`：邀请、密码边界、重名、限流、会话过期/注销、A/B/匿名隔离、一次认领、v1重复迁移、UTC/重启额度、并发最后额度、插入失败回滚、Mock/runner一致、分享哨兵/撤销/删除/跨owner/活跃409与会话容量。
- `apps/web/src/` 新增账户面板、历史管理、报告分享、公共报告及密码字节校验测试；`api.ts`统一接口与结构化额度错误。登录/退出完整重载清理内存和旧Run选择；存储不写密码、邀请码、Key或任务token。`vite.config.ts`保留Host以校验Origin。
- `scripts/stage3_smoke.py`：临时DB、随机端口和随机测试凭据；真实浏览器完整流程、重启持久化、桌面/移动端截图、慢响应匿名会话检查及原smoke隔离转发。
- README/API文档、010规格、011后续支付监控计划：配置、迁移/备份/回滚、权限、额度含义和未实现依赖。
- 根代理提供 `.github/workflows/ci.yml`：PR目标扩至main与codex/feature/**，原push范围和检查保留，追加前端生产构建与新邀请浏览器gate；部署workflow不变。

## 红→绿

1. `go test ./... -run 'TestInviteRegistrationAndIsolation|TestInviteSecureStartRequiresLogin' -count=1`：实现前注册实际404/期望403，secure匿名启动实际202/期望401；实现后通过。
2. `go test ./... -run 'TestInviteRejectedAuthDoesNotCreateSessions|TestInviteSessionCapacityAndCleanup' -count=1`：修复前无效凭据签发持久化cookie，4096匿名会话后仍返回200；修复后无cookie失败/429会话行数保持0、桶数有界，满额503且有限清理恢复，认证容量独立保留。
3. 撤销竞态：`TestInviteRevokeClaimTransactionRace`在归属读取后让注册认领确定性等待同一数据库事务，成功撤销后公开404；反向顺序旧owner为404，不返回虚假成功。根代理已独立通过定向race。
4. 移动端真实复跑曾发现随机连续Run ID撑宽最近实验区域；补min-width/换行后恢复，保留原scrollWidth断言。

## 可复跑门禁

从仓库根目录运行，Node依赖与Playwright/Chromium须已安装：

```bash
cd services/api
go mod verify
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
cd ../../apps/web
npm run lint
npm run check
npm audit --omit=dev --audit-level=high
cd ../..
python3 scripts/stage3_smoke.py
python3 scripts/stage3_smoke.py --legacy
git diff --check
```

2026-11-18最终本地门禁全部通过：仅改动Go文件gofmt；go mod verify全部验证、vet通过、完整test 10.070s、完整race 23.916s；前端lint及check通过（4文件10测试、typecheck和生产构建），生产依赖audit 0漏洞；新smoke与慢身份响应检查、原smoke隔离fixture均通过；git diff --check无错误。

## 浏览器证据

邀请注册→Mock真实本地HTTP采样→报告→分享复制→独立无账户浏览器只读读取→API重启后账户/额度/分享保留→撤销404→再次分享→额度耗尽不变更预留→删除Run与实验→关联分享404→退出/登录→刷新身份恢复。

独立慢响应检查延迟真实`auth/me`响应：App/History/StrictMode初始仅一次me；身份未就绪的提交不创建实验；随后首次匿名实验和刷新均200，没有晚到cookie改换归属。测试未替换后端数据或伪造成功响应。

`--legacy` 原样执行 `scripts/ui_smoke.py`；浏览器通过临时HTTP转发fixture把固定4173 origin送到临时Go服务，保留Host/Origin和所有旧断言。旧runner测试的受控route stub保持原样；没有触碰旧服务。

截图默认在Python临时目录下`inferlab-stage3-evidence/`（可用`--output-dir`覆盖）：registration-mobile、report-desktop、report-mobile、public-mobile、history-mobile。已目视检查并以scrollWidth断言验证窄屏；密码/邀请均遮罩，无页面异常或敏感存储。

## 外部依赖

staging未验收；正式支付、监控和公开运营未实现。根代理已完成并验证堆叠PR的CI过滤和新smoke门禁，本次提交纳入该文件。远端检查以PR实际结果为准，可执行 `gh pr checks codex/feature/invite-test`；不得将本地通过表述为阶段或上线完成。
