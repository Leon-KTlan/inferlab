# 邀请测试任务进度

1. 目标：邀请身份、隔离、认领、持久化配额、脱敏分享及删除的本地完整闭环；staging 验证前不标阶段完成。
2. 基线：0b8fca6，工作区干净；已新建 codex/feature/invite-test，依赖 PR #4。
3. 所有权：本任务独占 services/api、apps/web、docs、README 和新 stage3_smoke.py 写入；规则/旧断言/CI/部署只读。
4. 顺序：010 规格 -> 身份迁移负面测试 -> 配额分享删除 -> 前端闭环 -> 完整门禁 -> 文档/PR/CI。
5. 最大风险：认领时活跃任务归属/额度、跨账号泄漏、分享 DTO 和配额事务竞态。
6. 基线通过：go test ./... -run 'TestStoreMigrationVersion|TestConfigValidation'；npm test（8/8）。
7. 已读根/目录规则及 008；未启动其它 AI；测试使用独立临时 DB 和端口。
8. 红→绿：TestInviteRegistrationAndIsolation 起初注册 404/期望403；TestInviteSecureStartRequiresLogin 起初匿名启动202/期望401；实现后连同旧生命周期测试通过（2.698s）。密码范围已按根审核改为12–72字节，身份写接口强制JSON，KDF前有界限流。
9. 已实现：身份/迁移/认领、事务额度、终态白名单分享及撤销/删除、前端账户/历史/公共页；根审查指出的撤销认领竞态与拒绝认证写会话均修复，新增确定性 race/容量负例。Vite保留原Host；慢 me 响应真实浏览器检查通过。
10. 交付：最终Go vet/test/race、web lint/check、audit、新smoke/慢me/原smoke隔离fixture全部通过；根代理CI已就绪并纳入提交。PR/CI查询 `gh pr view codex/feature/invite-test`、`gh pr checks codex/feature/invite-test`；证据见EVIDENCE.md。本批不合并/发布，待根验收。
