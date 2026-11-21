# 双执行进度

1. 目标：两种受控执行路径都生成可追溯报告，保留Mock与阶段图表；本地完成，待staging。
2. 任务0：规则、worktree/remote、固定4ea482b基线已核对；Go vet/test/race及前端18项基线全通过。
3. 隔离分支：codex/feature/dual-execution；依赖PR #10，原目录/图表工作树未提交内容未改。
4. 规格：013替代009的仅本地默认/手工文件默认/新Agent取消条款，旧CLI保留。
5. API/Worker/Agent：已实现受限内存任务、租约、单次码、配额、来源快照、取消/失联/重启失败；无schema迁移。
6. Web：显式选择平台/本地与授权，完整Agent任务、固定源码及SHA-256，不含Key/会话/长期task令牌。
7. 验证：最终152项Go（含子测试）及race，前端21项和四门禁；真实TLS双模式+原Mock图表浏览器全通过。
8. 隔离：Docker真实DROP计数探针与Uid/Gid65532、capabilities为0；密钥/DB/复制/日志边界有回归。
9. 源码：35424119f07c3cdb7e00efc8ec5fc863b02213eb公开归档摘要校验与构建通过；证据在evidence/。
10. Git/交付：全部实现/脚本/证据已push，Draft PR #11已创建/附加（依赖#10），本地预览4186已启动；高级手动CLI补全并通过未修改的原ui_smoke；代码提交d936388的GitHub四项CI全绿（github-ci.json），本次仅文档记录；无本地阻塞，staging/真实供应商见BLOCKED，不合并/上线。
