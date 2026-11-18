邀请测试用户现在可以注册/登录、管理本人实验、分享可撤销的脱敏终态报告，并按UTC日期原子预留每日请求额度。local保留匿名体验；staging/production创建任务需登录。依赖 #4，本PR堆叠在 `codex/feature/openai-compatible-runner`，不合并、不生产发布。

## 影响范围

- Go/SQLite：v2非破坏迁移；bcrypt 12–72字节密码；七天HttpOnly服务端会话、轮换/撤销、JSON/Origin约束和KDF前有界持久化限流。认证失败/429不写匿名会话，匿名与总会话容量有限。
- 注册可事务认领一次匿名实验、活跃任务、分享和累计额度；登录已有账户不自动合并。Runner凭据领取/结果提交保留任务token授权。
- Mock/本地任务共用创建Run事务，预留max_requests；耗尽结构化429，取消/删除不退。仅限制平台任务创建，不是供应商账单，也不能控制额外本地请求。
- 公共报告显式DTO白名单；分享创建/撤销持久化，撤销与认领串行；删除终态记录清理关联行并使分享404，活跃409，跨owner404。
- React账户、额度、历史删除确认、分享复制/撤销及无账号公共页；慢身份响应防止首次cookie竞态。保留Key输入和诊断流程。

## 验证

- Go：仅改动文件gofmt、`go mod verify`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`。
- Web：`npm run lint`、`npm run check`（10项测试）、`npm audit --omit=dev --audit-level=high`（0漏洞）。
- `python3 scripts/stage3_smoke.py`真实浏览器注册→Mock→分享→独立访客→重启→撤销→额度→删除→登录恢复；延迟真实me响应的首次匿名实验检查。
- `python3 scripts/stage3_smoke.py --legacy`原ui_smoke及断言不改，用临时转发fixture指向独立DB/端口；旧8080/4173未触碰。桌面/移动端截图与敏感存储检查。
- 新身份/门禁及会话容量负例留有真实红→绿；详见 `docs/tasks/invite-testing/EVIDENCE.md`。
- 根代理补齐CI：PR目标扩至main与codex/feature/**，保留所有原检查与push范围，追加生产构建和新邀请browser gate；部署workflow不变。提交后检查实际远端结果。

## 配置和迁移

运行环境注入 `INVITE_CODE`（未配置关闭注册）和可选 `DAILY_REQUEST_LIMIT`（默认10000）；现有secure环境仍要求 `SESSION_SECRET`。不要在仓库或日志记录配置值。新增 `golang.org/x/crypto`，随其依赖升级x/sys；Go要求仍为1.24。

部署前停止写入并做SQLite backup，v1→v2自动事务迁移，保留旧记录。v1无额度账本，预留从v2任务开始记录。反向代理必须保留Host；应用不信任XFF，因此代理下用户可能共享IP桶，须staging验证。匿名会话上限4096、总8192，有限清理后仍满时新会话503；无密码重置/找回。

## 风险和回滚

缺少staging证据，保持待根代理验收。正式支付/监控/公开运营属于后续计划，未启用。单实例SQLite，禁止多个API进程执行同一DB任务。迁移后回滚旧版本必须同时恢复迁移前DB备份与旧二进制，不能降低user_version；回滚会丢弃备份后的新注册/运行/分享，需停写后保留当前快照。

CI修改由根代理独占编写并交付本PR纳入；实现代理未改部署配置。正式发布须另做staging与回滚验证。
