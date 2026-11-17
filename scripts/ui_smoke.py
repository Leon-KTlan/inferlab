from playwright.sync_api import sync_playwright
import json


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1440, "height": 1000})
    page.goto("http://127.0.0.1:4173", wait_until="networkidle")
    page.screenshot(path="/tmp/inferlab-home-20261114.png", full_page=True)
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    assert page.get_by_text("InferLab", exact=True).is_visible()
    assert page.get_by_role("button", name="免费体验 Mock 体检").is_visible()
    page.get_by_role("button", name="免费体验 Mock 体检").click()
    assert page.get_by_text("建立一次受控体检", exact=True).is_visible()
    assert page.get_by_text("MOCK 模式", exact=True).is_visible()
    assert page.get_by_role("button", name="校验并运行实验").is_visible()
    page.get_by_label("并发阶梯").fill("1")
    page.get_by_label("每阶段秒数").fill("2")
    page.get_by_label("最大请求数").fill("100")
    page.get_by_label("最大 RPS").fill("50")
    page.get_by_label("单请求超时（毫秒）").fill("500")
    page.get_by_role("button", name="校验并运行实验").click()
    page.get_by_text("实验正在被真实执行", exact=True).wait_for(timeout=5000)
    page.get_by_role("button", name="查看基础报告").wait_for(timeout=10000)
    page.get_by_role("button", name="查看基础报告").click()
    page.get_by_text("基础体检报告", exact=True).wait_for(timeout=5000)
    assert page.get_by_text("阶段指标", exact=True).is_visible()

    page.get_by_role("button", name="新建实验").last.click()
    page.get_by_role("button", name="校验并运行实验").click()
    page.get_by_text("实验正在被真实执行", exact=True).wait_for(timeout=5000)
    page.get_by_role("button", name="查看基础报告").wait_for(timeout=10000)
    page.get_by_role("button", name="查看基础报告").click()
    page.get_by_text("对比另一次 Mock 运行", exact=True).wait_for(timeout=5000)
    page.get_by_role("button", name="查看差值").click()
    page.get_by_text("比较状态", exact=True).wait_for(timeout=5000)
    assert page.get_by_text("可比", exact=True).is_visible()
    browser.close()

    # The local Runner handoff uses a route stub so this check never calls a real API.
    browser = playwright.chromium.launch(headless=True)
    context = browser.new_context(viewport={"width": 390, "height": 844}, accept_downloads=True)
    context.grant_permissions(["clipboard-read", "clipboard-write"])
    page = context.new_page()
    runner_status = {"value": "queued"}
    runner_calls = {"create": 0}

    def runner_api(route):
        request = route.request
        if request.method == "GET" and request.url.endswith("/api/v1/runs"):
            route.fulfill(json={"runs": []})
        elif request.method == "POST" and request.url.endswith("/experiments"):
            route.fulfill(status=201, json={"experiment_id": "exp-local", "name": "Local runner", "target": {"type": "openai_compatible", "endpoint": "https://api.example.com/v1", "model": "demo"}, "workload": {"concurrency_steps": [1], "stage_seconds": 1, "max_requests": 10, "max_rps": 2, "timeout_ms": 1000}, "slo": {"p95_e2e_ms": 1000, "error_rate_max": 0.05}})
        elif request.method == "POST" and request.url.endswith("/experiments/exp-local/runner-tasks"):
            runner_calls["create"] += 1
            route.fulfill(status=202, json={"task_id": "run-local", "run_id": "run-local", "experiment_id": "exp-local", "source_type": "openai_compatible", "config": {"target": {"type": "openai_compatible", "endpoint": "https://api.example.com/v1", "model": "demo"}, "workload": {"concurrency_steps": [1]}, "slo": {"p95_e2e_ms": 1000, "error_rate_max": 0.05}}, "task_token": "smoke-capability-token", "result_endpoint": "/api/v1/runner-tasks/run-local/result"})
        elif request.method == "POST" and request.url.endswith("/runs/run-local/cancel"):
            runner_status["value"] = "canceled"
            route.fulfill(status=200, json={})
        elif request.method == "GET" and request.url.endswith("/runs/run-local"):
            route.fulfill(json={"run_id": "run-local", "experiment_id": "exp-local", "status": runner_status["value"], "source_type": "openai_compatible", "result_kind": "measured", "request_count": 0, "completed_count": 0, "success_count": 0, "error_count": 0, "p50_e2e_ms": 0, "p95_e2e_ms": 0, "error_rate": 0, "throughput_rps": 0, "duration_seconds": 0, "current_stage": 0, "stage_summaries": [], "config": {"workload": {"concurrency_steps": [1]}}})
        elif request.method == "GET" and request.url.endswith("/runs/run-local/events"):
            route.fulfill(json={"events": []})
        else:
            route.continue_()

    page.route("**/api/v1/**", runner_api)
    page.goto("http://127.0.0.1:4173", wait_until="networkidle")
    page.get_by_role("button", name="免费体验 Mock 体检").click()
    page.get_by_role("button", name="OpenAI 兼容 API").click()
    page.get_by_label("Endpoint").fill("https://api.example.com/v1")
    page.get_by_label("模型").fill("demo")
    page.get_by_label("我拥有目标服务的测试授权，并理解可能产生的流量与费用").check()
    with page.expect_download() as download_info:
        page.get_by_role("button", name="创建本地 Runner 任务").click()
    download = download_info.value
    with open(download.path(), encoding="utf-8") as task_file:
        task = json.load(task_file)
    assert task["task_token"] == "smoke-capability-token"
    assert download.suggested_filename == "task.json"
    assert "OPENAI_API_KEY" not in json.dumps(task)
    assert "smoke-capability-token" not in page.url
    assert page.evaluate("Object.values(localStorage).join(' ')").find("smoke-capability-token") == -1
    assert page.get_by_text("等待本地 Runner", exact=True).is_visible()
    assert page.get_by_text("cd services/api && go run . runner").is_visible()
    command = page.locator(".command-row code").inner_text()
    assert "--api-url http://127.0.0.1:4173 --authorize-target" in command
    with page.expect_download() as second_download:
        page.get_by_role("button", name="下载 task.json").click()
    assert second_download.value.suggested_filename == "task.json"
    page.get_by_role("button", name="复制 Runner 命令").click()
    assert page.evaluate("navigator.clipboard.readText()") == command
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    page.screenshot(path="/tmp/inferlab-runner-mobile-20261117.png", full_page=True)

    page.reload(wait_until="networkidle")
    page.get_by_text("本地 Runner 交接", exact=True).wait_for(timeout=5000)
    assert page.get_by_text("等待本地执行", exact=True).is_visible()
    assert page.get_by_text("刷新后任务能力令牌已清除", exact=False).is_visible()
    assert runner_calls["create"] == 1
    assert page.evaluate("Object.values(localStorage).join(' ')").find("smoke-capability-token") == -1
    assert page.get_by_text("Ctrl+C", exact=False).is_visible()
    page.get_by_role("button", name="取消记录").click()
    page.locator(".run-header .status").filter(has_text="已取消").wait_for(timeout=5000)
    browser.close()
