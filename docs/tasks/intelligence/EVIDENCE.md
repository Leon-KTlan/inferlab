# 智力检测与雷达验收

日期为2026-11-22。分支 codex/feature/intelligence-radar 基于毕设版 e8644da，保留其依赖的性能测试与账号能力。

## 功能证据

- [产品与来源](00-product-and-research.md) 记录了用户Chrome参考页、Simon Willison和LiveBench的可借鉴方法。
- [评分核心](01-suite.md) 实现五维十题与严格答案判定。reference通过率100，degraded通过率40，所有分数明确属于Mock场景。
- [后端](02-backend.md) 复用SQLite、归属、额度和终态写入。能力记录不能通过性能接口执行、比较、分享或导出。
- [网页](03-web.md) 完成创建、状态、逐题规则、复测、取消、刷新恢复和雷达。取消回执后重新读取完整记录。
- [本地Runner](04-local-runner.md) 已用fake TLS目标实际完成十题闭环、严格评分、限速、凭据领取和回传。没有执行真实供应商请求。

## 已执行检查

后端在 services/api 下运行 `gofmt -w .`、`go vet ./...`、`go test -count=1 ./...` 和 `go test -race -count=1 ./...` 均通过。前端在 apps/web 下运行 `npm run lint && npm run check` 通过，含7个测试文件、25项测试。

```bash
python3 -B scripts/intelligence_smoke.py --output /tmp/inferlab-intelligence-browser-03
docker build -f infra/Dockerfile -t inferlab:intelligence-preview .
```

浏览器脚本通过：三次reference与一次degraded、历史基线100与当前40、变化−60、逐题规则、刷新、复测取消、独立身份空历史、390px无整页横向溢出。容器构建成功。最终构建另在临时库和Caddy HTTPS下以APP_ENV=staging运行，Chromium确认登录门禁、Mock评分、私有报告和跨身份404。

## 独立审查及修复

本里程碑进行一次只读独立审查。已修复两项问题：

1. 真实结果回传未免建匿名会话，可能在匿名会话容量满时被阻断。现在只有精确POST结果路径免建会话，任务令牌仍必需。回归验证无cookie回传不增会话/不发cookie，4096匿名会话下有效结果仍被接收。
2. 初次创建后再遇额度或活跃上限可能留下隐藏实验。现在实验与Run在同一预留事务内写入，失败整体回滚。回归验证两类429后实验数量不变，成功与复测正常。

## 发布状态

本机预览用独立命名卷 inferlab-intelligence-preview-data 保存演示记录。远端GitHub staging尚无所需部署密钥，未执行公网部署。运行、授权与回滚按 [手册](RUNBOOK.md)。CI结果以本分支PR的检查记录为准。
