# Registration Settings Progress

- [x] 核对 bf7cb90 基线与目录规则，创建 codex/feature/registration-settings。
- [x] 写入规格和任务边界。
- [x] API 红绿测试、schema v3、初始化与注册管理。
- [x] 浏览器初始化和管理员设置界面。
- [x] 临时数据库完整 smoke 和旧流程回归。
- [x] Go vet/test/race、前端 lint/check、根聊天独立 API/浏览器验收。
- [x] 根聊天备份并更新 4184 本地预览，留给用户自行初始化。
- [ ] 提交、堆叠 PR 和最新 HEAD CI（提交后由 PR 检查记录）。
- [ ] staging 验证：未执行，不能标记上线完成。

范围：services/api、apps/web/src、docs、README、新 smoke；不修改 CI、锁文件、Runner/SSRF 或已有断言。不启动子代理、不合并、不部署。

例外归属：根聊天独占修改 .github/workflows/ci.yml，仅新增本批 smoke 门禁，明确要求纳入本批首次提交。根聊天负责本地 4184 备份与重启；执行聊天不操作原 DB 或进程。
