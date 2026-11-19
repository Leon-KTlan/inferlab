# 注册体验本地验收

基线33234c1；分支codex/feature/registration-flow，目标base为codex/feature/invite-test，依赖PR #5。只修改授权前端、测试及文档；后端、旧测试断言、CI/部署和lockfile保持只读。未启动其它AI，未使用旧数据库，未停止8080/4173/4184，未调用真实供应商。

## 行为和文件

- `apps/web/src/AccountPanel.tsx`：邀请注册入口始终可发现；关闭时显示真实说明和登录切换，无注册表单/请求。开放时新增确认密码，仅客户端内存；注册成功使用既有后端轮换会话并自动登录。切换/关闭/失败清理密码、确认密码、邀请码；提交期间字段和切换/关闭禁用，使用同步guard防重复。模态portal、背景inert、初始焦点、Tab约束、Esc及焦点恢复。
- `apps/web/src/registration.ts`、`registration.test.ts`：沿用用户名/UTF-8 12–72字节密码约束，确认密码完全一致，固定安全错误映射。旧`validateAccount`导出继续兼容旧测试，不改断言。
- `apps/web/src/styles.css`：仅注册表单、切换按钮、关闭状态及320窄屏样式。目视检查发现既有关闭按钮规则影响新模式按钮，已覆盖精确选择器，并加模式文字尺寸检查。
- `scripts/stage3_smoke.py`：仅新增检查（原有断言未删除/放松），既有注册步骤补填确认密码；新增独立临时fixture，注册关闭通过真实临时进程取消邀请配置实现。延迟真实注册成功响应验证pending行为；网络错误仅abort请求，不伪造成功。
- `docs/specs/010-invite-testing.md`及本目录：交互契约、进度、验证、限制和PR说明。

## 检查

基线 `npm test`：4文件10项通过。新增校验测试后5文件13项；运行：

```bash
cd apps/web
npm run lint
npm run check
cd ../..
python3 scripts/stage3_smoke.py --output-dir /tmp/inferlab-registration-evidence
python3 scripts/stage3_smoke.py --legacy
git diff --check
```

lint/check（typecheck、生产构建、13项测试）通过。最终新smoke（三条PASS：原邀请闭环、慢身份、注册补充）和`--legacy`均通过，git diff --check无错误。未改后端，不重复全量Go测试；新/旧浏览器fixture均自建Go二进制。原smoke原文件及全部断言保留，固定4173使用既有隔离转发fixture。

新增真实浏览器覆盖：

- 确认密码不一致时POST数量0并清空敏感字段；修正后只发一次注册，payload只有username/password/invite/claim_anonymous，不传确认密码，自动登录且刷新恢复。
- 重名409、邀请码403、实际第六次尝试429；固定中文提示且失败后清理字段；网络连接失败可理解，提示不回显请求内容。
- 登录/注册切换、关闭与Esc均清空敏感字段，保留用户名；注册与关闭状态合理初始焦点，Tab/Shift+Tab环绕，背景inert，关闭恢复入口焦点。
- 暂停真实响应时字段/切换/关闭禁用，Esc不关闭，重复requestSubmit不产生第二次请求；完成后自动登录。
- 临时服务真实关闭注册，入口可发现、关闭说明可见、无注册表单及新增POST；已有账户仍能真实登录。
- 桌面1440、移动390/320水平无溢出，切换按钮文字尺寸检查；localStorage/sessionStorage不含密码/确认密码/邀请码，无pageerror。

## 截图

位置 `/tmp/inferlab-registration-evidence/`：`registration-open-desktop.png`、`registration-open-390.png`、`registration-open-320.png`、`registration-closed-desktop.png`、`registration-closed-390.png`、`registration-closed-320.png`；另保留原邀请闭环截图。密码、确认密码及邀请均遮罩。

## 限制和回滚

继续邀请制，关闭时不能注册，不增加邮箱/短信/第三方登录或找回；服务开放状态以`auth/me`为准，若配置在打开页面后变化，后端仍最终拒绝并提示邀请无效或注册暂停。原预览4184未改配置。

无新增依赖、配置或数据库迁移。回滚只需恢复原前端构建；后端邀请/账户数据不受此批影响。本批仅堆叠PR，不合并/生产发布。已通过根代理本地独立验收，仍待后续staging证据，不能称线上完成。

## 根代理独立验收

根代理报告：独立`npm run check`通过13/13测试、typecheck和生产构建；完整`stage3_smoke.py`（原邀请闭环、慢身份、新注册场景）通过，截图在`/tmp/inferlab-registration-root`。320开放和桌面关闭截图已目视检查，切换按钮CSS修复在构建产物生效，无新增发现。上述摘要与实现一次提交，不追加纯验收提交；远端最终HEAD以PR查询 `gh pr checks codex/feature/registration-flow` 为准。
