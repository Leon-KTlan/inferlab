"""Isolated real-browser acceptance. No supplier requests or existing DB access.

python3 scripts/stage3_smoke.py
python3 scripts/stage3_smoke.py --legacy
"""

import argparse
import asyncio
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import runpy
import secrets
import socket
import subprocess
import tempfile
import threading
import time
from urllib.parse import urlsplit
from urllib.request import urlopen

from playwright.sync_api import BrowserType, expect, sync_playwright
from playwright.async_api import async_playwright, expect as async_expect


ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Fixture:
    def __init__(self, directory, legacy):
        self.port = free_port()
        self.base = f"http://127.0.0.1:{self.port}"
        self.binary = directory / "inferlab-test-api"
        self.log = open(directory / "api.log", "w", encoding="utf-8")
        self.env = dict(os.environ)
        self.env.update(APP_ENV="local", DB_PATH=str(directory / "fixture.db"),
                        LISTEN_ADDR=f"127.0.0.1:{self.port}",
                        WEB_DIR=str(ROOT / "apps/web/dist"),
                        INVITE_CODE=secrets.token_urlsafe(24),
                        SESSION_SECRET=secrets.token_urlsafe(40),
                        DAILY_REQUEST_LIMIT="10000" if legacy else "100")
        self.process = None
        subprocess.run(["go", "build", "-o", str(self.binary), "."], cwd=ROOT / "services/api", check=True)

    def start(self):
        self.process = subprocess.Popen([str(self.binary)], cwd=ROOT, env=self.env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError("isolated API exited before readiness")
            try:
                with urlopen(self.base + "/healthz", timeout=1) as response:
                    if response.status == 200:
                        return
            except OSError:
                time.sleep(0.05)
        raise RuntimeError("isolated API readiness timed out")

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)


def assert_safe(page, forbidden):
    overflow = page.evaluate("""() => [...document.querySelectorAll('*')].filter(e => e.getBoundingClientRect().right > innerWidth + 1).slice(0, 8).map(e => ({tag: e.tagName, className: e.className, right: e.getBoundingClientRect().right}))""")
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"horizontal overflow: {overflow}"
    stored = page.evaluate("JSON.stringify([Object.entries(localStorage), Object.entries(sessionStorage)])")
    assert all(value not in stored for value in forbidden), "sensitive browser storage"


def invite_flow(fixture, output):
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 1440, "height": 1000})
        context.grant_permissions(["clipboard-read", "clipboard-write"])
        page = context.new_page()
        errors, server_errors = [], []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on("response", lambda response: server_errors.append(response.status) if response.status >= 500 else None)
        page.goto(fixture.base, wait_until="networkidle")
        username = "smoke_" + secrets.token_hex(5)
        password = secrets.token_urlsafe(24)
        forbidden = [password, fixture.env["INVITE_CODE"]]
        page.get_by_role("button", name="邀请注册", exact=True).click()
        dialog = page.get_by_role("dialog")
        dialog.get_by_label("用户名", exact=True).fill(username)
        dialog.get_by_label("密码", exact=True).fill(password)
        dialog.get_by_label("确认密码", exact=True).fill(password)
        dialog.get_by_label("邀请码", exact=True).fill(fixture.env["INVITE_CODE"])
        page.set_viewport_size({"width": 390, "height": 844})
        assert_safe(page, forbidden)
        # Password and invite stay masked in the review screenshot.
        page.screenshot(path=str(output / "registration-mobile.png"), full_page=True)
        dialog.get_by_role("button", name="注册并登录").click()
        expect(page.get_by_role("region", name="账户与额度").get_by_text(username, exact=True)).to_be_visible()
        page.reload(wait_until="networkidle")
        expect(page.get_by_text(username, exact=True)).to_be_visible()
        cookie = next(cookie for cookie in context.cookies() if cookie["name"] == "inferlab_session")
        assert cookie["httpOnly"] and cookie["sameSite"] == "Strict"
        page.set_viewport_size({"width": 1440, "height": 1000})
        page.get_by_role("button", name="免费体验 Mock 体检").click()
        private_name = "private-smoke-report"
        page.get_by_label("实验名称", exact=True).fill(private_name)
        page.get_by_label("并发阶梯").fill("3")
        page.get_by_label("每阶段秒数").fill("2")
        page.get_by_label("最大请求数").fill("60")
        page.get_by_label("最大 RPS").fill("30")
        page.get_by_label("单请求超时（毫秒）").fill("500")
        page.get_by_role("button", name="校验并运行实验").click()
        page.get_by_role("button", name="查看基础报告").wait_for(timeout=12000)
        run_id = page.evaluate("localStorage.getItem('inferlab:last-run')")
        page.get_by_role("button", name="查看基础报告").click()
        expect(page.get_by_text("基础体检报告", exact=True)).to_be_visible()
        expect(page.get_by_text("60 / 100", exact=True)).to_be_visible()
        page.get_by_role("button", name="分享脱敏报告", exact=True).click()
        share = page.get_by_label("报告分享链接")
        expect(share).to_be_visible()
        share_url = share.input_value()
        page.get_by_role("button", name="复制分享链接", exact=True).click()
        expect(page.get_by_text("链接已复制。", exact=True)).to_be_visible()
        assert page.evaluate("navigator.clipboard.readText()") == share_url
        assert_safe(page, forbidden)
        page.screenshot(path=str(output / "report-desktop.png"), full_page=True)
        page.set_viewport_size({"width": 390, "height": 844})
        assert_safe(page, forbidden)
        page.screenshot(path=str(output / "report-mobile.png"), full_page=True)
        page.set_viewport_size({"width": 1440, "height": 1000})

        guest = browser.new_context(viewport={"width": 390, "height": 844})
        public = guest.new_page()
        public.on("pageerror", lambda error: errors.append(str(error)))
        public.goto(share_url, wait_until="networkidle")
        expect(public.get_by_role("heading", name="脱敏体检报告")).to_be_visible()
        expect(public.get_by_role("heading", name="阶段指标")).to_be_visible()
        assert private_name not in public.content() and username not in public.content()
        assert guest.cookies() == [], "public report should not require a session"
        assert_safe(public, forbidden)
        public.screenshot(path=str(output / "public-mobile.png"), full_page=True)
        private = guest.request.get(fixture.base + "/api/v1/runs/" + run_id)
        assert private.status == 404

        fixture.stop()
        fixture.start()
        public.reload(wait_until="networkidle")
        expect(public.get_by_role("heading", name="阶段指标")).to_be_visible()
        page.reload(wait_until="networkidle")
        expect(page.get_by_text(username, exact=True)).to_be_visible()
        expect(page.get_by_text("60 / 100", exact=True)).to_be_visible()
        page.get_by_role("button", name="报告", exact=True).click()
        page.get_by_role("button", name="撤销分享", exact=True).click()
        expect(page.get_by_text("分享已撤销，旧链接已失效。", exact=True)).to_be_visible()
        public.reload(wait_until="networkidle")
        expect(public.get_by_role("alert")).to_contain_text("报告不可用")
        page.get_by_role("button", name="分享脱敏报告", exact=True).click()
        expect(page.get_by_label("报告分享链接")).to_be_visible()
        replacement = page.get_by_label("报告分享链接").input_value()
        assert replacement != share_url

        page.get_by_role("button", name="新建实验", exact=True).first.click()
        page.get_by_role("button", name="校验并运行实验").click()
        expect(page.get_by_text("每日预留请求额度不足", exact=False)).to_be_visible()
        expect(page.get_by_text("60 / 100", exact=True)).to_be_visible()
        page.get_by_role("button", name="工作台", exact=True).click()
        page.set_viewport_size({"width": 390, "height": 844})
        history = page.get_by_role("region", name="历史管理")
        expect(history.get_by_role("button", name=f"删除运行 {run_id}", exact=True)).to_be_visible()
        assert_safe(page, forbidden)
        page.screenshot(path=str(output / "history-mobile.png"), full_page=True)
        page.on("dialog", lambda dialog: dialog.accept())
        history.get_by_role("button", name=f"删除运行 {run_id}", exact=True).click()
        expect(history.get_by_role("button", name=f"删除运行 {run_id}", exact=True)).to_have_count(0)
        public.goto(replacement, wait_until="networkidle")
        expect(public.get_by_role("alert")).to_contain_text("报告不可用")
        history.get_by_role("button", name=f"删除实验 {private_name}", exact=True).click()
        expect(history.get_by_role("button", name=f"删除实验 {private_name}", exact=True)).to_have_count(0)
        expect(page.get_by_text("60 / 100", exact=True)).to_be_visible()
        page.get_by_role("button", name="退出", exact=True).click()
        expect(page.get_by_text("匿名试用", exact=True)).to_be_visible()
        page.get_by_role("button", name="登录", exact=True).click()
        dialog = page.get_by_role("dialog")
        dialog.get_by_label("用户名", exact=True).fill(username)
        dialog.get_by_label("密码", exact=True).fill(password)
        dialog.get_by_role("button", name="登录", exact=True).click()
        expect(page.get_by_text(username, exact=True)).to_be_visible()
        page.reload(wait_until="networkidle")
        expect(page.get_by_text(username, exact=True)).to_be_visible()
        expect(page.get_by_text("60 / 100", exact=True)).to_be_visible()
        assert_safe(page, forbidden)
        assert errors == [], errors
        assert server_errors == [], server_errors
        browser.close()
    print("PASS: registration, Mock, share, isolated guest, restart, revoke, quota, delete, login, refresh; desktop/mobile screenshots")


def legacy_flow(fixture):
    # Keep ui_smoke.py and every assertion intact. An HTTP forwarding fixture
    # maps its fixed browser origin to the isolated API, preserving Host/Origin.
    class Forwarder(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def forward(self):
            parsed = urlsplit(self.path)
            if parsed.hostname != "127.0.0.1" or parsed.port != 4173:
                self.send_error(502)
                return
            connection = http.client.HTTPConnection("127.0.0.1", fixture.port, timeout=15)
            headers = {name: value for name, value in self.headers.items() if name.lower() not in {"proxy-connection", "connection"}}
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            try:
                path = parsed.path + (("?" + parsed.query) if parsed.query else "")
                connection.request(self.command, path, body, headers)
                response = connection.getresponse()
                data = response.read()
                self.send_response(response.status)
                for name, value in response.getheaders():
                    if name.lower() not in {"connection", "transfer-encoding", "content-length"}:
                        self.send_header(name, value)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            finally:
                connection.close()

        do_GET = forward
        do_POST = forward
        do_DELETE = forward

    proxy = ThreadingHTTPServer(("127.0.0.1", 0), Forwarder)
    thread = threading.Thread(target=proxy.serve_forever, daemon=True)
    thread.start()
    original_launch = BrowserType.launch

    def isolated_launch(browser_type, *args, **kwargs):
        kwargs["proxy"] = {"server": f"http://127.0.0.1:{proxy.server_port}"}
        kwargs["args"] = [*kwargs.get("args", []), "--proxy-bypass-list=<-loopback>"]
        return original_launch(browser_type, *args, **kwargs)

    BrowserType.launch = isolated_launch
    try:
        runpy.run_path(str(ROOT / "scripts/ui_smoke.py"), run_name="__main__")
    finally:
        BrowserType.launch = original_launch
        proxy.shutdown()
        proxy.server_close()
        thread.join(timeout=5)
    print("PASS: original scripts/ui_smoke.py unchanged, isolated forwarding fixture; existing 8080/4173 services untouched")


async def registration_flow(fixture, output):
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True)
        context = await browser.new_context(viewport={"width": 1440, "height": 1000})
        page = await context.new_page()
        errors, posts = [], []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on("request", lambda request: posts.append(request) if request.method == "POST" and request.url.endswith("/auth/register") else None)
        username = "register_" + secrets.token_hex(4)
        password = secrets.token_urlsafe(24)
        mismatch = secrets.token_urlsafe(24)
        invitation = fixture.env["INVITE_CODE"]
        forbidden = [password, mismatch, invitation]

        async def safe_storage():
            assert await page.evaluate("document.documentElement.scrollWidth <= innerWidth"), "registration horizontal overflow"
            assert await page.locator(".auth-modes button").evaluate_all("elements => elements.every(element => element.clientWidth >= 90 && element.scrollHeight <= element.clientHeight && element.scrollWidth <= element.clientWidth)"), "account mode labels do not fit"
            stored = await page.evaluate("JSON.stringify([Object.entries(localStorage), Object.entries(sessionStorage)])")
            assert all(value not in stored for value in forbidden), "registration secrets persisted"

        async def fill_registration(name, invite=invitation, confirmation=None):
            dialog = page.get_by_role("dialog")
            await dialog.get_by_label("用户名", exact=True).fill(name)
            await dialog.get_by_label("密码", exact=True).fill(password)
            await dialog.get_by_label("确认密码", exact=True).fill(password if confirmation is None else confirmation)
            await dialog.get_by_label("邀请码", exact=True).fill(invite)

        async def fields_cleared(register=True):
            dialog = page.get_by_role("dialog")
            await async_expect(dialog.get_by_label("密码", exact=True)).to_have_value("")
            if register:
                await async_expect(dialog.get_by_label("确认密码", exact=True)).to_have_value("")
                await async_expect(dialog.get_by_label("邀请码", exact=True)).to_have_value("")

        release, submitted = asyncio.Event(), asyncio.Event()

        async def hold_registration(route):
            # Hold an actual success response; never replace the API outcome.
            response = await route.fetch()
            submitted.set()
            await release.wait()
            await route.fulfill(response=response)

        try:
            await page.goto(fixture.base, wait_until="networkidle")
            launcher = page.get_by_role("button", name="邀请注册", exact=True)
            await launcher.click()
            dialog = page.get_by_role("dialog")
            await async_expect(dialog.get_by_label("用户名", exact=True)).to_be_focused()
            assert await page.locator(".app-shell").evaluate("element => element.inert")
            close = dialog.get_by_role("button", name="关闭账户窗口")
            submit = dialog.get_by_role("button", name="注册并登录", exact=True)
            await close.focus()
            await page.keyboard.press("Shift+Tab")
            await async_expect(submit).to_be_focused()
            await page.keyboard.press("Tab")
            await async_expect(close).to_be_focused()
            await fill_registration(username)
            await dialog.get_by_role("button", name="切换到登录", exact=True).click()
            await fields_cleared(register=False)
            await async_expect(dialog.get_by_label("用户名", exact=True)).to_have_value(username)
            await dialog.get_by_label("密码", exact=True).fill(password)
            await dialog.get_by_role("button", name="切换到邀请注册", exact=True).click()
            await fields_cleared()
            await fill_registration(username)
            await close.click()
            await async_expect(launcher).to_be_focused()
            await launcher.click()
            await fields_cleared()
            await async_expect(dialog.get_by_label("用户名", exact=True)).to_have_value(username)
            await fill_registration(username)
            await page.keyboard.press("Escape")
            await async_expect(dialog).to_have_count(0)
            await async_expect(launcher).to_be_focused()
            assert not await page.locator(".app-shell").evaluate("element => element.inert")
            await launcher.click()
            await fields_cleared()
            await fill_registration(username, confirmation=mismatch)
            await submit.click()
            await async_expect(dialog.get_by_role("alert")).to_have_text("两次输入的密码不一致，请重新填写。")
            await fields_cleared()
            assert len(posts) == 0, "mismatched confirmation made a registration request"
            await fill_registration(username)
            for width, height, name in [(1440, 1000, "desktop"), (390, 844, "390"), (320, 740, "320")]:
                await page.set_viewport_size({"width": width, "height": height})
                await safe_storage()
                await page.screenshot(path=str(output / f"registration-open-{name}.png"), full_page=True)
            await page.route("**/api/v1/auth/register", hold_registration)
            await submit.click()
            await asyncio.wait_for(submitted.wait(), timeout=5)
            await async_expect(dialog).to_have_attribute("aria-busy", "true")
            await async_expect(close).to_be_disabled()
            await async_expect(dialog.get_by_label("密码", exact=True)).to_be_disabled()
            await async_expect(dialog.get_by_role("button", name="切换到登录", exact=True)).to_be_disabled()
            await page.keyboard.press("Escape")
            await async_expect(dialog).to_be_visible()
            await page.keyboard.press("Tab")
            await async_expect(dialog).to_be_focused()
            await dialog.locator("form").evaluate("form => form.requestSubmit()")
            assert len(posts) == 1, "pending registration submitted twice"
            assert set(posts[0].post_data_json) == {"username", "password", "invite", "claim_anonymous"}, "confirmation sent to the server"
            release.set()
            await async_expect(page.get_by_role("region", name="账户与额度").get_by_text(username, exact=True)).to_be_visible()
            await page.reload(wait_until="networkidle")
            await async_expect(page.get_by_text(username, exact=True)).to_be_visible()
            await safe_storage()
            await page.unroute("**/api/v1/auth/register", hold_registration)
            await page.get_by_role("button", name="退出", exact=True).click()
            await async_expect(page.get_by_text("匿名试用", exact=True)).to_be_visible()
            await launcher.click()
            await fill_registration(username)
            await submit.click()
            await async_expect(dialog.get_by_role("alert")).to_have_text("此用户名已被使用，请换一个用户名。")
            await fields_cleared()
            await async_expect(dialog.get_by_label("用户名", exact=True)).to_have_value(username)
            await fill_registration("other_" + secrets.token_hex(4), invite="invalid-test-invitation")
            await submit.click()
            await async_expect(dialog.get_by_role("alert")).to_have_text("邀请码无效或注册已暂停，请确认邀请信息后重试。")
            await fields_cleared()
            for _ in range(2):
                await fill_registration("other_" + secrets.token_hex(4), invite="invalid-test-invitation")
                await submit.click()
                await async_expect(dialog.get_by_role("alert")).to_have_text("邀请码无效或注册已暂停，请确认邀请信息后重试。")
                await fields_cleared()
            await fill_registration("limited_" + secrets.token_hex(4))
            await submit.click()
            await async_expect(dialog.get_by_role("alert")).to_have_text("尝试次数过多，请在十分钟后重试。")
            await fields_cleared()
            await safe_storage()
            assert len(posts) == 6

            async def network_failure(route):
                await route.abort("connectionfailed")

            await dialog.get_by_role("button", name="切换到登录", exact=True).click()
            await dialog.get_by_label("密码", exact=True).fill(password)
            await page.route("**/api/v1/auth/login", network_failure)
            await dialog.get_by_role("button", name="登录", exact=True).click()
            await async_expect(dialog.get_by_role("alert")).to_have_text("无法连接服务，请检查网络后重试。")
            await fields_cleared(register=False)
            await page.unroute("**/api/v1/auth/login", network_failure)
            await page.keyboard.press("Escape")

            # Real server configuration closes registration; no success is stubbed.
            fixture.stop()
            fixture.env["INVITE_CODE"] = ""
            fixture.start()
            await page.reload(wait_until="networkidle")
            await async_expect(page.get_by_role("region", name="账户与额度").get_by_text("注册暂未开放", exact=True)).to_be_visible()
            await launcher.click()
            await async_expect(dialog.get_by_role("heading", name="注册暂未开放", exact=True)).to_be_visible()
            await async_expect(dialog.locator("form")).to_have_count(0)
            await async_expect(close).to_be_focused()
            before = len(posts)
            for width, height, name in [(1440, 1000, "desktop"), (390, 844, "390"), (320, 740, "320")]:
                await page.set_viewport_size({"width": width, "height": height})
                await safe_storage()
                await page.screenshot(path=str(output / f"registration-closed-{name}.png"), full_page=True)
            await dialog.get_by_role("button", name="前往登录", exact=True).click()
            await dialog.get_by_label("用户名", exact=True).fill(username)
            await dialog.get_by_label("密码", exact=True).fill(password)
            await dialog.get_by_role("button", name="登录", exact=True).click()
            await async_expect(page.get_by_text(username, exact=True)).to_be_visible()
            assert len(posts) == before, "closed registration sent a POST"
            await safe_storage()
            assert errors == [], errors
        finally:
            release.set()
            await browser.close()
    print("PASS: registration confirmation, automatic login, duplicate/invite/rate/network errors, secret cleanup, pending guard, keyboard focus/Esc, closed registration, 1440/390/320 layouts")


async def slow_identity_flow(fixture):
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True)
        page = await browser.new_page()
        release = asyncio.Event()
        entered = asyncio.Event()
        me_calls, creates = [], []

        async def delay_identity(route):
            me_calls.append(route.request.url)
            response = await route.fetch()
            entered.set()
            await release.wait()
            await route.fulfill(response=response)

        await page.route("**/api/v1/auth/me", delay_identity)
        page.on("request", lambda request: creates.append(request.url) if request.method == "POST" and request.url.endswith("/experiments") else None)
        try:
            await page.goto(fixture.base, wait_until="domcontentloaded")
            await asyncio.wait_for(entered.wait(), timeout=5)
            await page.get_by_role("button", name="免费体验 Mock 体检").click()
            await page.get_by_label("并发阶梯").fill("1")
            await page.get_by_label("每阶段秒数").fill("1")
            await page.get_by_label("最大请求数").fill("10")
            await page.get_by_role("button", name="校验并运行实验").click()
            await async_expect(page.get_by_text("账户状态尚未读取，请稍后重试。", exact=True)).to_be_visible()
            assert creates == [], "experiment created before identity was ready"
            assert len(me_calls) == 1, "App/History/StrictMode created concurrent anonymous identities"
            release.set()
            await async_expect(page.get_by_text("匿名试用", exact=True)).to_be_visible()
            await page.get_by_role("button", name="校验并运行实验").click()
            await page.get_by_role("button", name="查看基础报告").wait_for(timeout=12000)
            run_id = await page.evaluate("localStorage.getItem('inferlab:last-run')")
            assert len(creates) == 1
            response = await page.request.get(fixture.base + "/api/v1/runs/" + run_id)
            assert response.status == 200, "late session cookie orphaned the first experiment"
            await page.reload(wait_until="networkidle")
            response = await page.request.get(fixture.base + "/api/v1/runs/" + run_id)
            assert response.status == 200, "first experiment lost after reload"
        finally:
            release.set()
            await browser.close()
    print("PASS: delayed real identity response, one initial me request, early-submit guard, first anonymous experiment survives reload")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--legacy", action="store_true", help="run the unchanged original UI smoke through an isolated forwarding fixture")
    parser.add_argument("--output-dir", type=Path, default=Path(tempfile.gettempdir()) / "inferlab-stage3-evidence")
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    if not (ROOT / "apps/web/dist/index.html").exists():
        raise RuntimeError("build the web app first: cd apps/web && npm run build")
    with tempfile.TemporaryDirectory(prefix="inferlab-stage3-") as directory:
        fixture = Fixture(Path(directory), args.legacy)
        try:
            fixture.start()
            if args.legacy:
                legacy_flow(fixture)
            else:
                invite_flow(fixture, args.output_dir)
                asyncio.run(slow_identity_flow(fixture))
                with tempfile.TemporaryDirectory(prefix="inferlab-registration-") as registration_dir:
                    registration = Fixture(Path(registration_dir), False)
                    try:
                        registration.start()
                        asyncio.run(registration_flow(registration, args.output_dir))
                    finally:
                        registration.stop()
                        registration.log.close()
        finally:
            fixture.stop()
            fixture.log.close()


if __name__ == "__main__":
    main()
