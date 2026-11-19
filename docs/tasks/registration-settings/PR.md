首次访问本地 InferLab 现在可初始化独立管理员；管理员在站点设置开放邀请注册、替换邀请码，访客用有效码注册并自动登录。已有账户迁移为普通用户，初始化不会提升或覆盖旧账户。依赖 #6；目标 base 为 codex/feature/registration-flow。

范围：schema v3/角色、一次性事务初始化、服务端管理权限、注册设置与审计、账户弹窗、独立浏览器 smoke、规格/交接；CI 追加由根聊天完成并授权统一提交。没有修改既有 smoke 断言、锁文件、Runner/SSRF 或部署配置。

验证：Go vet/test/race、前端 lint/check（14 tests）、registration_settings_smoke、stage3_smoke 及 --legacy 全通过；根另完成独立 API 与 320/390/1440 浏览器验收。证据见 docs/tasks/registration-settings/EVIDENCE.md。

配置/迁移：无新依赖；非 local 或 secure 初始化必须提供 ADMIN_SETUP_TOKEN，公网不暴露无管理员状态。新邀请码 8–256 UTF-8 字节，旧 INVITE_CODE 兼容；首次保存后 DB 优先。仅持久化邀请码摘要，审计不记录秘密。

风险/回滚：升级前 SQLite backup；旧二进制不支持 v3，回滚需停止写入并使用升级前备份的新工作副本，不覆盖唯一备份。代理后的用户共享实际连接 IP 限流。仅本地验收，未部署云端，staging 与真实 HTTPS 代理验证仍待完成。4184 原 DB 已由根备份后更新，初始化留给用户；安全步骤见 RESTART.md。
