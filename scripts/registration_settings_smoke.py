"""Real browser initialization/settings acceptance against a fresh isolated API.

python3 scripts/registration_settings_smoke.py
Requires the production web build, Python Playwright and Chromium.
"""

import argparse
import asyncio
from pathlib import Path
import secrets
import sqlite3
import tempfile

from playwright.async_api import async_playwright, expect

from stage3_smoke import Fixture


async def safe(page, forbidden):
    assert await page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "horizontal overflow"
    stored = await page.evaluate("JSON.stringify([Object.entries(localStorage), Object.entries(sessionStorage)])")
    assert all(value not in stored for value in forbidden), "sensitive browser storage"
    assert all(value not in page.url for value in forbidden), "sensitive URL"


async def registration(page, username, password, invite):
    await page.get_by_role("button", name="邀请注册", exact=True).click()
    dialog = page.get_by_role("dialog")
    await dialog.get_by_label("用户名", exact=True).fill(username)
    await dialog.get_by_label("密码", exact=True).fill(password)
    await dialog.get_by_label("确认密码", exact=True).fill(password)
    await dialog.get_by_label("邀请码", exact=True).fill(invite)
    return dialog


async def workflow(fixture, output):
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True)
        admin = await browser.new_context(viewport={"width": 1440, "height": 1000})
        await admin.grant_permissions(["clipboard-read", "clipboard-write"])
        page = await admin.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(type(error).__name__))
        password, invite, replacement = (secrets.token_urlsafe(24) for _ in range(3))
        username = "admin_" + secrets.token_hex(5)
        visitor_name = "visitor_" + secrets.token_hex(5)
        forbidden = [password, invite, replacement]
        await page.goto(fixture.base, wait_until="networkidle")
        await expect(page.get_by_text("站点尚未初始化", exact=True)).to_be_visible()
        opener = page.get_by_role("button", name="初始化站点", exact=True)
        await opener.click()
        dialog = page.get_by_role("dialog")
        await expect(dialog.get_by_label("用户名", exact=True)).to_be_focused()
        # Focus wraps and Escape restores the opener.
        await dialog.get_by_role("button", name="关闭账户窗口").focus()
        await page.keyboard.press("Shift+Tab")
        await expect(dialog.get_by_role("button", name="创建管理员并登录")).to_be_focused()
        await page.keyboard.press("Escape")
        await expect(opener).to_be_focused()
        await opener.click()
        await dialog.get_by_label("用户名", exact=True).fill(username)
        await dialog.get_by_label("密码", exact=True).fill(password)
        await dialog.get_by_label("确认密码", exact=True).fill(password + "different")
        setup_posts = []
        page.on("request", lambda req: setup_posts.append(1) if req.method == "POST" and req.url.endswith("/auth/setup") else None)
        await dialog.get_by_role("button", name="创建管理员并登录").click()
        await expect(dialog.get_by_role("alert")).to_contain_text("两次输入")
        assert not setup_posts, "invalid confirmation reached server"
        assert await dialog.get_by_label("密码", exact=True).input_value() == ""
        await dialog.get_by_label("密码", exact=True).fill(password)
        await dialog.get_by_label("确认密码", exact=True).fill(password)
        for width in (390, 320):
            await page.set_viewport_size({"width": width, "height": 844})
            await safe(page, forbidden)
            await page.screenshot(path=str(output / f"setup-{width}.png"), full_page=True)
        await dialog.get_by_role("button", name="创建管理员并登录").click()
        await expect(page.get_by_text(username, exact=True)).to_be_visible()
        await expect(page.get_by_text("管理员", exact=True)).to_be_visible()
        assert len(setup_posts) == 1
        await expect(page.get_by_text("站点尚未初始化", exact=True)).to_have_count(0)
        cookie = next(cookie for cookie in await admin.cookies() if cookie["name"] == "inferlab_session")
        assert cookie["httpOnly"] and cookie["sameSite"] == "Strict"

        await page.get_by_role("button", name="站点设置", exact=True).click()
        await expect(dialog.get_by_role("heading", name="注册管理")).to_be_visible()
        await expect(dialog.get_by_text("邀请码：未配置", exact=False)).to_be_visible()
        await dialog.get_by_label("开放邀请注册").check()
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_role("alert")).to_contain_text("先设置邀请码")
        # Client validation clears new secrets; no saving of a short code.
        await dialog.get_by_label("新邀请码", exact=True).fill("short")
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_role("alert")).to_contain_text("8–256")
        assert await dialog.get_by_label("新邀请码", exact=True).input_value() == ""
        await dialog.get_by_role("button", name="随机生成邀请码").click()
        generated = await dialog.get_by_label("新邀请码", exact=True).input_value()
        assert len(generated) == 48
        forbidden.append(generated)
        await dialog.get_by_role("button", name="显示新邀请码").click()
        assert await dialog.get_by_label("新邀请码", exact=True).get_attribute("type") == "text"
        await dialog.get_by_role("button", name="隐藏新邀请码").click()
        await dialog.get_by_role("button", name="复制新邀请码").click()
        assert await page.evaluate("navigator.clipboard.readText()") == generated
        await page.evaluate("navigator.clipboard.writeText('')")
        await dialog.get_by_label("新邀请码", exact=True).fill(invite)

        # Hold a real completed response to inspect disabled/loading behavior.
        entered, release = asyncio.Event(), asyncio.Event()

        async def hold(route):
            response = await route.fetch()
            entered.set()
            await release.wait()
            await route.fulfill(response=response)

        await page.route("**/api/v1/admin/registration", hold)
        await dialog.get_by_role("button", name="保存设置").click()
        await asyncio.wait_for(entered.wait(), timeout=10)
        await expect(dialog.get_by_role("button", name="正在保存…")).to_be_disabled()
        await expect(dialog.get_by_role("button", name="关闭账户窗口")).to_be_disabled()
        await page.keyboard.press("Escape")
        await expect(dialog).to_be_visible()
        release.set()
        await expect(dialog.get_by_text("设置已保存。", exact=True)).to_be_visible()
        await page.unroute("**/api/v1/admin/registration", hold)
        assert await dialog.get_by_label("新邀请码", exact=True).input_value() == ""
        await expect(dialog.get_by_text("来源：数据库设置", exact=False)).to_be_visible()
        for width in (1440, 390, 320):
            await page.set_viewport_size({"width": width, "height": 1000 if width == 1440 else 844})
            await safe(page, forbidden)
            await page.screenshot(path=str(output / f"settings-{width}.png"), full_page=True)
        await dialog.get_by_role("button", name="关闭账户窗口").click()
        await expect(page.get_by_text("注册暂未开放", exact=True)).to_have_count(0)

        visitor = await browser.new_context(viewport={"width": 390, "height": 844})
        guest = await visitor.new_page()
        guest.on("pageerror", lambda error: errors.append(type(error).__name__))
        await guest.goto(fixture.base, wait_until="networkidle")
        guest_dialog = await registration(guest, visitor_name, password, invite)
        await guest_dialog.get_by_role("button", name="注册并登录").click()
        await expect(guest.get_by_text(visitor_name, exact=True)).to_be_visible()
        await expect(guest.get_by_text("普通用户", exact=True)).to_be_visible()
        await expect(guest.get_by_role("button", name="站点设置", exact=True)).to_have_count(0)
        for method in ("get", "put"):
            response = await getattr(visitor.request, method)(fixture.base + "/api/v1/admin/registration", data={"enabled": False} if method == "put" else None)
            assert response.status == 403, "ordinary user reached settings"

        await page.get_by_role("button", name="站点设置", exact=True).click()
        await dialog.get_by_label("新邀请码", exact=True).fill(replacement)
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_text("设置已保存。", exact=True)).to_be_visible()
        await guest.reload(wait_until="networkidle")
        await expect(guest.get_by_text(visitor_name, exact=True)).to_be_visible()
        fresh = await browser.new_context(viewport={"width": 320, "height": 844})
        newcomer = await fresh.new_page()
        await newcomer.goto(fixture.base, wait_until="networkidle")
        new_name = "new_" + secrets.token_hex(5)
        newcomer_dialog = await registration(newcomer, new_name, password, invite)
        await newcomer_dialog.get_by_role("button", name="注册并登录").click()
        await expect(newcomer_dialog.get_by_role("alert")).to_contain_text("邀请码无效")
        assert await newcomer_dialog.get_by_label("邀请码", exact=True).input_value() == ""
        await safe(newcomer, forbidden)
        await newcomer.screenshot(path=str(output / "old-invite-error-320.png"), full_page=True)
        await newcomer_dialog.get_by_label("密码", exact=True).fill(password)
        await newcomer_dialog.get_by_label("确认密码", exact=True).fill(password)
        await newcomer_dialog.get_by_label("邀请码", exact=True).fill(replacement)
        await newcomer_dialog.get_by_role("button", name="注册并登录").click()
        await expect(newcomer.get_by_text(new_name, exact=True)).to_be_visible()

        # Failure after a real settings UI render must clear sensitive fields and retain the saved state.
        async def offline(route):
            await route.abort("connectionfailed")

        await dialog.get_by_label("新邀请码", exact=True).fill(invite)
        await page.route("**/api/v1/admin/registration", offline)
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_role("alert")).to_contain_text("无法连接")
        assert await dialog.get_by_label("新邀请码", exact=True).input_value() == ""
        await page.unroute("**/api/v1/admin/registration", offline)
        await dialog.get_by_label("开放邀请注册").uncheck()
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_text("设置已保存。", exact=True)).to_be_visible()
        await dialog.get_by_role("button", name="关闭账户窗口").click()
        assert not (await (await admin.request.get(fixture.base + "/api/v1/auth/me")).json())["registration_open"]
        await expect(page.get_by_text("注册暂未开放", exact=True)).to_be_visible()
        fixture.stop()
        fixture.env["INVITE_CODE"] = secrets.token_urlsafe(24)
        forbidden.append(fixture.env["INVITE_CODE"])
        fixture.start()
        await page.reload(wait_until="networkidle")
        await expect(page.get_by_text(username, exact=True)).to_be_visible()
        assert not (await (await admin.request.get(fixture.base + "/api/v1/auth/me")).json())["registration_open"]
        await expect(page.get_by_text("注册暂未开放", exact=True)).to_be_visible()
        # New visitor registration is denied after restart even though env invites are present.
        await newcomer.get_by_role("button", name="退出", exact=True).click()
        await newcomer.get_by_role("button", name="邀请注册", exact=True).click()
        await expect(newcomer.get_by_role("heading", name="注册暂未开放", exact=True)).to_be_visible()
        await safe(newcomer, forbidden)

        await page.get_by_role("button", name="退出", exact=True).click()
        await page.get_by_role("button", name="登录", exact=True).click()
        await dialog.get_by_label("用户名", exact=True).fill(username)
        await dialog.get_by_label("密码", exact=True).fill(password)
        await dialog.get_by_role("button", name="登录", exact=True).click()
        await expect(page.get_by_role("button", name="站点设置", exact=True)).to_be_visible()
        await page.get_by_role("button", name="站点设置", exact=True).click()
        await expect(dialog.get_by_label("开放邀请注册")).not_to_be_checked()
        await expect(dialog.get_by_text("邀请码：已配置", exact=False)).to_be_visible()
        await dialog.get_by_label("开放邀请注册").check()
        await dialog.get_by_role("button", name="保存设置").click()
        await expect(dialog.get_by_text("设置已保存。", exact=True)).to_be_visible()
        result = await admin.request.get(fixture.base + "/api/v1/admin/registration")
        assert await result.json() == {"enabled": True, "invite_configured": True, "source": "database"}
        for active in (page, guest, newcomer):
            await safe(active, forbidden)
        assert not errors, "browser JavaScript error"
        await browser.close()
        return forbidden


async def token_entry(fixture, output):
    """Non-local deployment mode requires a token even on loopback; status stays private."""
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True)
        context = await browser.new_context(viewport={"width": 390, "height": 844})
        page = await context.new_page()
        await page.goto(fixture.base, wait_until="networkidle")
        await expect(page.get_by_text("站点尚未初始化", exact=True)).to_have_count(0)
        await page.get_by_role("button", name="登录", exact=True).click()
        await page.get_by_role("button", name="初始化站点", exact=True).click()
        dialog = page.get_by_role("dialog")
        password = secrets.token_urlsafe(24)
        await dialog.get_by_label("用户名", exact=True).fill("operator_" + secrets.token_hex(5))
        await dialog.get_by_label("密码", exact=True).fill(password)
        await dialog.get_by_label("确认密码", exact=True).fill(password)
        await dialog.get_by_label("部署初始化令牌").fill(fixture.env["ADMIN_SETUP_TOKEN"])
        await safe(page, [password, fixture.env["ADMIN_SETUP_TOKEN"]])
        await page.screenshot(path=str(output / "token-setup-mobile.png"), full_page=True)
        await dialog.get_by_role("button", name="创建管理员并登录").click()
        await expect(page.get_by_text("管理员", exact=True)).to_be_visible()
        await safe(page, [password, fixture.env["ADMIN_SETUP_TOKEN"]])
        await browser.close()
        return [password, fixture.env["ADMIN_SETUP_TOKEN"]]


def check_storage(directory, forbidden):
    # Read only the isolated fixture. Never print database rows, logs, or secret values.
    with sqlite3.connect(directory / "fixture.db") as db:
        assert db.execute("PRAGMA user_version").fetchone()[0] == 3
        for table, in db.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall():
            rows = repr(db.execute('SELECT * FROM "' + table.replace('"', '""') + '"').fetchall())
            assert all(value not in rows for value in forbidden), "plaintext credential in database"
    log = (directory / "api.log").read_text()
    assert all(value not in log for value in forbidden), "credential in API log"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=Path("/tmp/inferlab-registration-settings-evidence"))
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    for token_mode in (False, True):
        with tempfile.TemporaryDirectory(prefix="inferlab-settings-") as temp:
            directory = Path(temp)
            fixture = Fixture(directory, False)
            fixture.env.update(INVITE_CODE="", ADMIN_SETUP_TOKEN="")
            if token_mode:
                fixture.env.update(APP_ENV="operator-test", ADMIN_SETUP_TOKEN=secrets.token_urlsafe(32))
            try:
                fixture.start()
                forbidden = asyncio.run(token_entry(fixture, args.output) if token_mode else workflow(fixture, args.output))
                fixture.stop()
                check_storage(directory, forbidden)
            finally:
                fixture.stop()
                fixture.log.close()
    print("PASS registration settings: fresh setup, roles, invitations, restart, token UI, storage and responsive checks")


if __name__ == "__main__":
    main()
