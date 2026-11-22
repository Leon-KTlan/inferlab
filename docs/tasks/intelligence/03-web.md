# 智力检测与智商雷达网页

状态：实现与本地前端门禁通过；浏览器闭环、CLI 联调与 staging 验收由根代理记录。

## 变更与边界

- `apps/web/src/App.tsx`：新增“智力检测”“智商雷达”侧栏入口。保留性能检测流程，能力数据使用独立 client；hash 为 `#intelligence`、`#intelligence?run=<run_id>`、`#radar`。性能运行恢复不会覆盖能力页。
- `intelligence-api.ts`：直接使用原始 Run 与 `/api/v1/intelligence` 契约；客户端输入校验、专用 CLI 命令、仅包含任务字段的下载文件、可比性判断。
- `IntelligencePage.tsx`：按日期分组的卡片历史、Mock reference/degraded、重复 1–3（默认 2）、真实 HTTPS 目标与显式授权、运行详情、取消、按快照复测、任务下载与逐题复核。题目和期望答案取自 `/suite`，不保存或展示原始回答。兼容未完成报告的 `outcomes: null`。
- `RadarPage.tsx`：模型、Endpoint、题库及来源/重复次数分组选择；五轴 SVG 配可访问数值表，最近三次完整历史的分维均值、基线差值和时间趋势；保留全部历史状态与 Mock profile。
- `intelligence.css`：延续绿色与米白的测量工具风格，支持窄屏、焦点可见与减少动态效果偏好。
- `intelligence.test.ts`：9 个最小测试，覆盖验证、路由、接口路径与授权隔离、连接失败、任务文件、可比性边界与图表数值表。

只允许 `succeeded`、无运行错误、完整且 `error_count=0` 的检测参与比较。当前记录不可比时不显示旧分数或误报退化；少于三条历史时显示样本不足。读取失败保留快照并提示可能过期。无排行榜、计费推断或生成代码执行。

API Key 不进入 localStorage、sessionStorage、URL 或任务文件；创建成功或失败后清空。真实复测必须重新授权且不复用 Key。task_token 仅保存在当前页面内存，刷新或离开清除。任务创建成功自动下载，当前页可再次下载；刷新后说明“已下载可继续，未下载先取消旧记录后重建”。网页取消只关闭真实记录，需终端 Ctrl+C 停止目标请求。创建、取消、复测与观察到终态时刷新账户配额。

## 验证

在 `apps/web` 执行：

```bash
npm run lint
npm run check
```

结果：TypeScript、生产构建、Vitest 全部通过，7 个测试文件 / 24 个测试。此轮完整 check 在处理 radar 全历史与 `outcomes: null` 后重跑通过；后续取消接口联调修复另补 1 个用例，按下文最小集合验证。

浏览器联调入口与 selectors：

- 侧栏按钮：“智力检测”“智商雷达”。页面：`[data-testid="intelligence-page"]`、`[data-testid="radar-page"]`。
- 创建：`[data-testid="new-intelligence"]`；表单 `aria-label="新建智力检测"`；“开始 Mock 检测”“授权并创建本地任务”。
- 字段 `aria-label`：“检测模型”“每题重复次数”“Mock 场景”“智力检测 API Key”。
- 历史：`[data-testid="intelligence-history-card"]`。详情 `aria-label="智力检测详情"`；“取消检测”“同快照复测”“重新授权并复测”。
- 任务：“下载智力检测 task.json”；交接区域 `aria-label="智力检测本地 Runner"`。
- 雷达筛选为原生 label/select；图表有 `role="img"` 与数值表。逐题规则为原生 details/summary。

## 依赖、限制与回滚

依赖后端 014 契约及 `intelligence-runner`。真实目标未授权时不会创建任务。客户端不能代替后端 SSRF、权限与配额校验；浏览器/CLI/staging 结果由根代理补记，本文不宣称已经通过。

无数据库与环境变量变更。回滚时撤回以上前端新增文件和 App 入口改动并重新构建，保留后端历史数据。没有提交、推送或部署。

联调修复：取消接口实际返回 `{run_id,status,cancel_requested}` 回执；client 在 POST 成功后 GET 完整 Run，避免将缺少 config 的回执写入详情。新增回执三字段后读取完整快照的回归测试。详情可比性使用 `isComparableIntelligence(run)`，分别提示未完成、请求错误或状态不能比较。修复后 `npm test -- src/intelligence.test.ts`（9 个测试）和 `npm run build`（含 TypeScript）通过。
