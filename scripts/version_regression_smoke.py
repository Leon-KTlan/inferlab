"""Exercise baseline persistence and repeat runs against the real local API."""

from pathlib import Path
import tempfile

from playwright.sync_api import expect, sync_playwright

from stage3_smoke import Fixture


def workflow(fixture):
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 390, "height": 844})
        page = context.new_page()
        page.goto(fixture.base)

        def run_mock(stage_seconds, rps=40):
            page.get_by_role("button", name="工作台", exact=True).click()
            page.get_by_role("button", name="免费体验 Mock 体检").click()
            page.get_by_label("并发阶梯").fill("4")
            page.get_by_label("每阶段秒数").fill(str(stage_seconds))
            page.get_by_label("最大请求数").fill("150")
            page.get_by_label("最大 RPS").fill(str(rps))
            page.get_by_label("单请求超时（毫秒）").fill("1000")
            page.get_by_role("button", name="校验并运行实验").click()
            page.get_by_role("button", name="查看基础报告").click(timeout=15000)

        run_mock(2)
        panel = page.get_by_role("region", name="版本回归体检", exact=True)
        panel.get_by_role("button", name="设为基线").click()
        expect(panel.get_by_text("当前报告是基线")).to_be_visible()
        baseline = page.evaluate("localStorage.getItem('inferlab:last-run')")
        page.reload()
        page.get_by_role("button", name="报告", exact=True).click()
        expect(panel.get_by_text("当前报告是基线")).to_be_visible()
        panel.get_by_role("button", name="按基线复测").click()
        page.get_by_role("button", name="查看基础报告").click(timeout=15000)
        candidate = page.evaluate("localStorage.getItem('inferlab:last-run')")
        assert candidate and candidate != baseline
        panel.get_by_role("button", name="对比已保存基线").click()
        expect(panel.get_by_role("status")).to_contain_text("本次采样")
        for width in (390, 1440):
            page.set_viewport_size({"width": width, "height": 844})
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")

        run_mock(1, 5)
        panel.get_by_role("button", name="对比已保存基线").click()
        expect(panel.get_by_role("alert")).to_contain_text("不可比较")
        expect(panel.get_by_role("status")).to_have_count(0)
        panel.get_by_role("button", name="设为基线").click()
        panel.get_by_role("button", name="按基线复测").click()
        page.get_by_role("button", name="查看基础报告").click(timeout=15000)
        panel.get_by_role("button", name="对比已保存基线").click()
        expect(panel.get_by_role("alert")).to_contain_text("证据不足")
        expect(panel.get_by_role("status")).to_have_count(0)

        stranger = browser.new_context()
        unowned = stranger.request.get(fixture.base + f"/api/v1/reports/{candidate}/compare?baseline_run_id={baseline}")
        assert unowned.status == 404
        panel.get_by_role("button", name="清除基线").click()
        expect(panel.get_by_text("尚未保存基线")).to_be_visible()
        browser.close()


if __name__ == "__main__":
    with tempfile.TemporaryDirectory(prefix="inferlab-regression-") as directory:
        fixture = Fixture(Path(directory), legacy=True)
        try:
            fixture.env.update(PAYMENTS_ENABLED="false")
            fixture.start()
            workflow(fixture)
        finally:
            fixture.stop()
            fixture.log.close()
    print("Version regression browser smoke passed")
