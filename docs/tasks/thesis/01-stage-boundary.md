# Mock 阶段收尾与计时口径

## 行为

- 网页 Mock 到达阶段截止或用完请求预算后停止派发，已在途请求仍按各自的超时处理，收齐后才切换下一档。用户取消和持久化失败仍会取消在途请求。
- 阶段配置时长是发送窗口。报告中的阶段观测时长包含收尾等待，吞吐量按完成数除以实际观测秒数。真实 API 的本地 Runner 保持原有阶段截止与全局时限。
- 新 Mock Run 记录 `software_version=0.1.1`，旧报告保留原版本。现有严格对比禁止将两种阶段统计版本放到同一基线组。

## 验证

```bash
cd services/api
go test -count=1 -run '^(TestRunnerStageBoundary|TestRunnerConcurrencyAndRPS|TestAPILifecyclePersistenceIsolation|TestRunnerCLICancelAndTimeout|TestComparePersistedRuns)$' ./...
```

回归测试使一个请求跨过 1 秒阶段截止，检验它仍可正常完成，并验证等待收尾时手动取消能及时结束运行。旧实现把跨阶段请求记为失败，而且运行在取消命令前已结束。

## 风险与回滚

每个 Mock 阶段可能额外等待至多一个单请求超时窗口，加上本机调度和记录开销。演示时给轮询留出该时间。回滚到上一应用构建即可恢复旧行为。本阶段不含数据库迁移，旧报告数据仍可读取。
