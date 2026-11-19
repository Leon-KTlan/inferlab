"""Check real anonymous status, public policy and mobile navigation."""

from pathlib import Path
import tempfile

from playwright.sync_api import expect, sync_playwright

from stage3_smoke import Fixture


def workflow(fixture):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context()
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(type(error).__name__))
        for width in (1440, 390, 320):
            page.set_viewport_size({"width": width, "height": 844})
            page.goto(fixture.base + "/status")
            expect(page.get_by_role("status")).to_have_text("当前取样正常")
            expect(page.locator("time")).to_have_count(1)
            expect(page.get_by_text("支持入口暂未配置。")).to_be_visible()
            assert not context.cookies(), "public status created a session"
            response = context.request.get(fixture.base + "/api/v1/status")
            assert response.ok and set(response.json()) == {"status", "checked_at"}
            assert response.headers["cache-control"] == "no-store"
            assert context.request.get(fixture.base + "/internal/ops/metrics").status == 404
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "status overflow"
            page.get_by_role("link", name="条款与隐私草案").click()
            expect(page.get_by_role("heading", name="服务条款与目标授权")).to_be_visible()
            expect(page.get_by_text("运营主体、联系方式", exact=False)).to_be_visible()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "policy overflow"
        page.get_by_role("link", name="服务状态与支持").click()
        expect(page.get_by_role("status")).to_have_text("当前取样正常")
        fixture.stop()
        page.get_by_role("button", name="重新检查").click()
        expect(page.get_by_role("status")).to_have_text("无法确认当前状态")
        expect(page.locator("time")).to_have_count(0)
        assert not errors, errors
        browser.close()


if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as path:
        fixture = Fixture(Path(path), legacy=False)
        try:
            fixture.env.update(PAYMENTS_ENABLED="false", OPS_METRICS_TOKEN="", PUBLIC_SUPPORT_URL="")
            fixture.start()
            workflow(fixture)
        finally:
            fixture.stop()
            fixture.log.close()
    print("Public Beta browser smoke passed")
