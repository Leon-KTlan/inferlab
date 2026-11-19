邀请注册入口原先在服务关闭注册时完全隐藏，用户无法了解注册方式。本PR使入口始终可发现；关闭状态展示准确说明并保留登录入口，开放状态提供确认密码、清晰错误和完整键盘操作，注册成功继续自动登录。

依赖 #5，堆叠base `codex/feature/invite-test`。沿用邀请制username/password/invite，不改变后端或运行配置。

## 改动

- AccountPanel增加确认密码（只在客户端内存，完全一致才请求），按UTF-8 12–72字节校验；切换/关闭/失败清理所有敏感字段，保留用户名。
- 登录/注册可切换；关闭状态没有可提交注册表单。提交时禁用操作并阻止重复请求，服务错误映射为固定中文提示，不回显敏感输入。
- 模态portal及背景inert、初始焦点、Tab/Shift+Tab约束、Esc及焦点恢复；注册相关样式覆盖桌面和390/320宽屏。
- 新校验测试及真实浏览器流程；stage3_smoke仅新增检查和必需的确认密码输入，所有旧断言保留。010规格和独立交接目录同步。

## 验证

- 基线10项；`npm run lint`、`npm run check`通过，现13项测试。
- `python3 scripts/stage3_smoke.py --output-dir /tmp/inferlab-registration-evidence`：真实注册、自动登录/刷新、确认不一致零POST、重名/邀请码/限流/网络错误、敏感字段清理、pending防重、键盘焦点、真实关闭注册零POST及仍可登录。
- `python3 scripts/stage3_smoke.py --legacy`原smoke隔离运行，原文件及断言不改，不触现有8080/4173/4184服务或旧DB，不访问真实供应商。
- 桌面1440和移动390/320截图、水平溢出/模式文字尺寸/敏感存储及pageerror检查；`git diff --check`。
- 后端未改，无需重复全量Go测试；浏览器fixture实际构建并运行API。详见`docs/tasks/registration-flow/EVIDENCE.md`。
- 根代理独立验收：npm check 13/13、typecheck/build及完整stage3_smoke通过，320开放/桌面关闭截图已目视检查，无新增发现；独立证据`/tmp/inferlab-registration-root`。

## 配置、风险和回滚

无新增依赖、环境变量或数据库迁移；注册是否开放继续由服务端INVITE_CODE配置决定。本PR不修改现有预览的运行配置。若页面打开后服务端关闭注册，提交仍由后端拒绝并给出准确提示。

回滚恢复旧前端构建即可，账户数据和后端不变。staging未验证，本批不合并、不上线，待根代理独立验收；不宣称线上完成。
