"""Exercise intelligence history, scoring and radar using an isolated Mock API."""

import argparse
from pathlib import Path
import re
import tempfile
import time

from playwright.sync_api import expect, sync_playwright
from stage3_smoke import Fixture


def workflow(fixture, output):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 1280, "height": 960})
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(fixture.base + "/#intelligence")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_test_id("intelligence-page")).to_be_visible()
        expect(page.get_by_text("第一条能力曲线，从这里开始")).to_be_visible()

        def request(path, data=None):
            url = fixture.base + "/api/v1/intelligence" + path
            response = context.request.get(url) if data is None else context.request.post(url, data=data)
            assert response.ok, (response.status, response.text())
            return response.json()

        def finished(run_id):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                run = request("/runs/" + run_id)
                if run["status"] == "succeeded":
                    return run
                assert run["status"] in {"queued", "running"}, run["status"]
                time.sleep(.2)
            raise AssertionError("Mock did not finish within its request window")

        for index in range(3):
            result = request("/runs", {
                "name": f"Reference {index + 1}", "target": {"type": "mock", "model": "mock-llm"},
                "mock_profile": "reference", "repetitions": 1,
            })
            assert finished(result["run"]["run_id"])["intelligence_report"]["score"] == 100

        page.get_by_test_id("new-intelligence").click()
        form = page.get_by_role("region", name="新建智力检测")
        form.get_by_label("检测名称").fill("Degraded smoke")
        form.get_by_label("每题重复次数").select_option("1")
        form.get_by_label("Mock 场景").select_option("degraded")
        form.get_by_role("button", name="开始 Mock 检测").click()
        expect(page).to_have_url(re.compile(r"#intelligence\?run="))
        page.reload()
        page.get_by_role("button", name="同快照复测").wait_for(timeout=20000)
        expect(page.get_by_text("40.0", exact=True)).to_be_visible()
        page.get_by_text("有向先后关系", exact=False).click()
        expect(page.get_by_text("CAB", exact=True)).to_be_visible()
        page.screenshot(path=str(output / "intelligence-detail.png"), full_page=True)

        page.locator(".sidebar").get_by_role("button", name="智商雷达", exact=True).click()
        expect(page.get_by_test_id("radar-page")).to_be_visible()
        expect(page.get_by_text("值得复核", exact=True)).to_be_visible()
        expect(page.get_by_text("-60.0 分", exact=True)).to_be_visible()
        page.screenshot(path=str(output / "radar-desktop.png"), full_page=True)
        page.set_viewport_size({"width": 390, "height": 844})
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "radar overflow"
        page.screenshot(path=str(output / "radar-mobile.png"), full_page=True)

        page.locator(".sidebar").get_by_role("button", name="智力检测", exact=True).click()
        page.get_by_test_id("intelligence-history-card").first.click()
        page.get_by_role("button", name="同快照复测").click()
        page.get_by_role("button", name="取消检测").click()
        page.get_by_role("button", name="同快照复测").wait_for(timeout=10000)
        expect(page.get_by_text("此检测已取消", exact=False)).to_be_visible()
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "detail overflow"
        other = browser.new_context()
        private_runs = other.request.get(fixture.base + "/api/v1/intelligence/runs")
        assert private_runs.ok and private_runs.json()["runs"] == []
        other.close()
        assert not errors, errors
        browser.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="new screenshot directory")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix="inferlab-intelligence-smoke-") as temp:
        fixture = Fixture(Path(temp), legacy=False)
        fixture.env.update(DAILY_REQUEST_LIMIT="10000", PAYMENTS_ENABLED="false", OPS_METRICS_TOKEN="")
        try:
            fixture.start()
            workflow(fixture, args.output)
        finally:
            fixture.stop()
            fixture.log.close()
    print("Intelligence browser smoke passed: scoring, refresh, radar baseline, cancel, isolation, mobile")


if __name__ == "__main__":
    main()
