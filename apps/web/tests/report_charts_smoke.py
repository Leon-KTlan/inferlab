"""Real local Mock report plus explicitly synthetic boundary-response fixtures.

Build apps/web first, then run: python3 apps/web/tests/report_charts_smoke.py
Requires Python Playwright and Chromium. No existing database or real target API.
"""

import argparse
from copy import deepcopy
import json
from pathlib import Path
import sys
import tempfile

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "scripts"))
from stage3_smoke import Fixture


def check_width(page, width):
    page.set_viewport_size({"width": width, "height": 1000 if width > 680 else 844})
    page.wait_for_function("""() => {
        const svg = document.querySelector('.latency-chart svg');
        return !svg || Math.abs(svg.viewBox.baseVal.width - svg.getBoundingClientRect().width) < 1;
    }""")
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"overflow at {width}px"
    chart = page.locator(".latency-chart")
    assert chart.bounding_box()["width"] <= width
    assert page.get_by_role("heading", name="阶段指标", exact=True).is_visible()


def workflow(fixture, output):
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 1440, "height": 1000})
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(fixture.base, wait_until="networkidle")
        page.get_by_role("button", name="免费体验 Mock 体检").click()
        page.get_by_label("并发阶梯").fill("1, 5, 10, 20")
        page.get_by_label("每阶段秒数").fill("2")
        page.get_by_label("最大请求数").fill("400")
        page.get_by_label("最大 RPS").fill("40")
        page.get_by_role("button", name="校验并运行实验").click()
        page.get_by_role("button", name="查看基础报告").click(timeout=20000)
        chart = page.get_by_role("region", name="并发与 P95 延迟", exact=True)
        expect(chart.locator("circle")).to_have_count(4)
        expect(page.locator(".report-head .mock-badge")).to_contain_text("MOCK")

        run_id = page.evaluate("localStorage.getItem('inferlab:last-run')")
        response = context.request.get(fixture.base + f"/api/v1/runs/{run_id}/report")
        assert response.ok
        report = response.json()
        assert report["source_type"] == "mock"
        assert [stage["concurrency"] for stage in report["stage_summaries"]] == [1, 5, 10, 20]
        for index, stage in enumerate(report["stage_summaries"]):
            label = chart.locator("circle").nth(index).get_attribute("aria-label")
            assert f'并发 {stage["concurrency"]}，' in label
            plotted = float(label.split("P95 延迟 ")[1].split(" ms")[0].replace(",", ""))
            assert abs(plotted - stage["p95_e2e_ms"]) <= 0.0051
        summary_box = page.locator(".report-summary").bounding_box()
        chart_box = chart.bounding_box()
        table_box = page.locator(".table-wrap").first.bounding_box()
        assert summary_box["y"] + summary_box["height"] <= chart_box["y"] < table_box["y"]

        chart.locator("circle").first.focus()
        page.keyboard.press("Tab")
        assert chart.locator("circle").nth(1).evaluate("el => el === document.activeElement")
        expect(chart.locator(".latency-chart-readout")).to_contain_text("并发 5")
        page.keyboard.press("Tab")
        expect(chart.locator(".latency-chart-readout")).to_contain_text("并发 10")
        for width in (1440, 390, 320):
            check_width(page, width)
            chart.screenshot(path=str(output / f"mock-chart-{width}.png"))
            page.screenshot(path=str(output / f"mock-report-{width}.png"), full_page=True)

        # Reload and reopen the same real report: no chart-only cache or fake data.
        page.reload(wait_until="networkidle")
        page.get_by_role("button").filter(has_text=run_id).first.click()
        page.get_by_role("button", name="查看基础报告").click()
        expect(chart.locator("circle")).to_have_count(4)

        # Only the report response is stubbed below; these are test fixtures.
        stub = deepcopy(report)
        page.route(f"**/runs/{run_id}/report", lambda route: route.fulfill(json=stub))
        original = report["stage_summaries"]
        cases = {
            "empty": [],
            "single": [deepcopy(original[0])],
            "zero": [{**stage, "p95_e2e_ms": 0} for stage in original],
            "gap": [{**stage, "p95_e2e_ms": None if index == 1 else stage["p95_e2e_ms"]} for index, stage in enumerate(original)],
        }
        for name, stages in cases.items():
            check_width(page, 1440)
            stub["stage_summaries"] = stages
            page.get_by_role("button", name="报告", exact=True).click()
            expect(chart.locator("circle")).to_have_count(len(stages) - (1 if name == "gap" else 0))
            if name == "empty":
                expect(chart).to_contain_text("暂无可绘制的阶段数据")
            elif name == "single":
                expect(chart.locator("polyline")).to_have_count(0)
                expect(chart).to_contain_text("仅一个有效点")
            elif name == "zero":
                expect(chart).to_contain_text("所有已采样阶段的 P95 为 0 ms")
            else:
                expect(chart.locator("polyline")).to_have_count(1)
                expect(chart).to_contain_text("已留断点")
            for width in (390, 320):
                check_width(page, width)
            chart.screenshot(path=str(output / f"fixture-{name}-320.png"))

        assert not errors, errors
        (output / "result.json").write_text(json.dumps({
            "result": "passed", "source": "isolated local Go Mock runner",
            "run_id": run_id, "stages": report["stage_summaries"],
            "widths": [1440, 390, 320], "fixture_cases": list(cases),
            "keyboard": "Tab moves between measured points and updates the readout",
            "reload": "same report restored", "page_errors": errors,
        }, ensure_ascii=False, indent=2) + "\n")
        browser.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path(tempfile.gettempdir()) / "inferlab-report-charts-evidence")
    output = parser.parse_args().output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="inferlab-chart-test-") as directory:
        fixture = Fixture(Path(directory), legacy=True)
        try:
            fixture.env.update(PAYMENTS_ENABLED="false")
            fixture.start()
            workflow(fixture, output)
        finally:
            fixture.stop()
            fixture.log.close()
    print(f"Report chart browser smoke passed; evidence: {output}")
