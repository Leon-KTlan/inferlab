# 注册体验任务进度

1. 基线33234c1，工作区干净；分支codex/feature/registration-flow，PR依赖#5、base codex/feature/invite-test。
2. 范围：AccountPanel、必要样式、新校验/测试、stage3_smoke只增检查、010规格及本目录；后端/旧断言/CI/部署/lockfile只读，无其它AI。
3. 目标：注册可发现、关闭状态真实说明、确认密码与清理、键盘焦点、自动登录；不改变邀请策略。
4. 顺序：规格与基线→最小验证测试→表单/键盘→真实浏览器→lint/check/新旧smoke→PR/CI→根独立验收。
5. 风险：关闭时误发注册请求、确认密码外传、切换残留敏感值、提交重复/焦点逃逸。
6. npm test基线4文件10项通过；新增3项校验/错误映射，共13项及lint/check通过。真实后端注册/错误/关闭/键盘/敏感存储检查与全部旧smoke已通过一次。
7. 目视截图发现原[aria-label]按钮宽度影响模式切换，已精确覆盖并增加文字尺寸检查；复跑新旧smoke全部通过，最终截图/tmp/inferlab-registration-evidence。仅临时DB与随机端口，不触旧服务或真实供应商。
8. 根独立验收：npm check 13/13、typecheck/build及完整stage3_smoke通过；/tmp/inferlab-registration-root截图已目视检查，无新增发现。准备一次提交全部证据与堆叠PR，随后只跟踪最终HEAD CI，不扩展功能。
