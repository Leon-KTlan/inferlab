# 双执行进度

1. 目标：公网 HTTPS 平台代跑与本地 Agent 交接都形成可追溯报告；保留 Mock/图表。
2. 顺序：基线 → spec → API/Worker/领取码 → Web/交接 → TLS E2E/安全 → 文档/PR。
3. 最大风险：凭据生命周期、重复领取、取消/重启后的重复发送、Worker 网络隔离。
4. 工作树：独立 codex/feature/dual-execution，固定基线 4ea482b；不触碰其它目录改动。
5. 依赖：PR #10 codex/feature/report-charts；本次 PR 堆叠其上。
6. 基线：前端 lint/typecheck/build/test 全通过，18 tests；lint 是 tsc，非 ESLint。
7. 基线：Go vet/test/race 全通过。
8. 当前：API/Worker/Agent、SSRF/配额/租约/取消/重启已实现；Go全量/race与前端20测试通过；双模式TLS浏览器1440/390/320通过；Docker丢弃与降权探针通过。
9. 禁止：真实供应商调用、云资源/生产修改、其它 AI、合并/线上部署。

10. 剩余：固定源码提交/摘要、最终统一验收、PR与交付记录；staging/真实供应商见BLOCKED。
