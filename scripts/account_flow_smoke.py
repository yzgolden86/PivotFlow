import json
import os
import tempfile

from playwright.sync_api import expect, sync_playwright


BASE_URL = os.environ.get("PIVOTFLOW_SMOKE_URL", "http://127.0.0.1:8089")
PASSWORD = os.environ.get("PIVOTFLOW_SMOKE_PASSWORD")
RUN_SYNC = os.environ.get("PIVOTFLOW_SMOKE_SYNC") == "1"


def main():
    if not PASSWORD:
        raise SystemExit("PIVOTFLOW_SMOKE_PASSWORD is required")

    artifact_dir = os.path.join(tempfile.gettempdir(), "pivotflow-account-flow")
    os.makedirs(artifact_dir, exist_ok=True)
    desktop_screenshot = os.path.join(artifact_dir, "desktop.jpeg")
    mobile_screenshot = os.path.join(artifact_dir, "mobile.jpeg")
    account_dialog_screenshot = os.path.join(artifact_dir, "account-dialog.jpeg")
    accounts_page_screenshot = os.path.join(artifact_dir, "accounts-page.jpeg")
    compact_accounts_screenshot = os.path.join(artifact_dir, "accounts-compact.jpeg")
    credential_dialog_screenshot = os.path.join(artifact_dir, "credential-dialog.jpeg")
    sync_dialog_screenshot = os.path.join(artifact_dir, "sync-dialog.jpeg")
    console_errors = []
    failed_responses = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on(
            "console",
            lambda message: console_errors.append(message.text) if message.type == "error" else None,
        )
        page.on(
            "response",
            lambda response: failed_responses.append({"status": response.status, "url": response.url})
            if response.status >= 400
            else None,
        )

        page.goto(f"{BASE_URL}/web/login.html")
        page.wait_for_load_state("networkidle")
        page.locator("#password").fill(PASSWORD)
        page.locator("#login-button").click()
        page.wait_for_url("**/web/console/")
        page.wait_for_load_state("networkidle")

        navigation = page.get_by_role("navigation", name="主导航")
        expect(navigation.get_by_role("link", name="站点管理", exact=True)).to_be_visible()
        expect(navigation.get_by_role("link", name="账号管理", exact=True)).to_be_visible()
        for removed_text in ["站点账号", "模型能力基线", "通知设置", "PivotFlow Core", "路由数据面"]:
            expect(navigation.get_by_text(removed_text, exact=True)).to_have_count(0)

        navigation.get_by_role("link", name="站点管理", exact=True).click()
        page.wait_for_url("**/#/sites")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="站点管理", exact=True)).to_be_visible()
        page.get_by_role("button", name="添加站点", exact=True).click()
        dialog = page.get_by_role("dialog", name="添加站点")
        expect(dialog).to_be_visible()
        expect(dialog.get_by_text("同时添加首个账号", exact=True)).to_be_visible()
        credential_mode = dialog.get_by_label("添加方式")
        expect(credential_mode.locator("option")).to_have_count(4)
        credential_mode.select_option("access_token")
        expect(dialog.get_by_text("填写用户个人中心的系统访问令牌，不是以 sk- 开头的模型调用 Key。", exact=True)).to_be_visible()
        credential_mode.select_option("api_key")
        expect(dialog.get_by_text("模型 API Key 会用于模型发现和自动创建渠道，不会执行余额刷新、签到或公告同步。", exact=True)).to_be_visible()
        dialog.get_by_role("button", name="关闭弹窗").click()

        page.goto(f"{BASE_URL}/web/console/#/accounts")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="账号管理", exact=True)).to_be_visible()
        page.wait_for_timeout(250)
        page.screenshot(path=accounts_page_screenshot, type="jpeg", quality=85)
        if RUN_SYNC:
            balance_button = page.get_by_role("button", name="余额", exact=True).first
            balance_button.click()
            expect(page.locator(".account-records")).to_be_visible()
            expect(page.get_by_text("正在加载账号", exact=True)).to_have_count(0)
            expect(balance_button).to_be_enabled(timeout=120_000)
        edit_buttons = page.locator('button[aria-label^="编辑 "]')
        if edit_buttons.count():
            edit_buttons.first.click()
            account_dialog = page.get_by_role("dialog", name="编辑账号")
            expect(account_dialog.get_by_label("账号名称")).to_be_visible()
            expect(account_dialog.get_by_text("更换登录凭证", exact=True)).to_have_count(0)
            account_dialog.screenshot(path=account_dialog_screenshot, type="jpeg", quality=85)
            account_dialog.get_by_role("button", name="关闭弹窗").click()
        credential_buttons = page.get_by_role("button", name="凭证", exact=True)
        if credential_buttons.count():
            credential_buttons.first.click()
            credential_dialog = page.locator('[role="dialog"][aria-label^="更新凭证"]')
            expect(credential_dialog.get_by_label("凭证类型")).to_be_visible()
            expect(credential_dialog.get_by_role("button", name="验证凭证", exact=True)).to_be_visible()
            credential_dialog.screenshot(path=credential_dialog_screenshot, type="jpeg", quality=85)
            credential_dialog.get_by_role("button", name="关闭弹窗").click()
        page.goto(f"{BASE_URL}/web/console/#/accounts?site_id=1&create=1")
        page.wait_for_load_state("networkidle")
        if page.get_by_role("dialog", name="添加账号").count():
            create_dialog = page.get_by_role("dialog", name="添加账号")
            expect(create_dialog.get_by_label("所属站点").locator("option")).not_to_have_count(0)
            create_dialog.get_by_role("button", name="关闭弹窗").click()

        page.goto(f"{BASE_URL}/web/console/#/checkins")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="签到中心", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="账号", exact=True)).to_have_class("is-active")
        expect(page.get_by_role("button", name="记录", exact=True)).to_be_visible()

        navigation.get_by_role("link", name="渠道与分发", exact=True).click()
        page.wait_for_url("**/#/channels")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="渠道与分发", exact=True)).to_be_visible()
        expect(page.get_by_role("link", name="从站点添加", exact=True)).to_have_count(0)
        page.get_by_role("button", name="同步站点渠道", exact=True).click()
        sync_dialog = page.get_by_role("dialog", name="同步站点渠道")
        expect(sync_dialog).to_be_visible()
        expect(sync_dialog.get_by_text("选择账号后，系统会刷新模型，并用模型 API Key 创建或更新对应路由渠道。", exact=True)).to_be_visible()
        if RUN_SYNC:
            enabled_accounts = sync_dialog.locator('.site-sync-row input[type="checkbox"]:not(:disabled)').count()
            sync_dialog.get_by_role("button", name="全选可用账号", exact=True).click()
            sync_dialog.get_by_role("button", name="开始同步", exact=True).click()
            expect(sync_dialog.locator(".site-sync-list")).to_be_visible()
            expect(sync_dialog.get_by_role("button", name="开始同步", exact=True)).to_be_enabled(timeout=120_000)
            expect(sync_dialog.locator(".site-sync-row .status-badge--success")).to_have_count(enabled_accounts)
        sync_dialog.screenshot(path=sync_dialog_screenshot, type="jpeg", quality=85)
        sync_dialog.get_by_role("button", name="关闭弹窗").click()
        if RUN_SYNC:
            expect(page.locator(".channel-row").first).to_be_visible(timeout=30_000)
        page.locator(".source-menu > summary").click()
        expect(page.get_by_role("button", name="手工渠道", exact=True)).to_be_visible()

        navigation.get_by_role("link", name="系统设置", exact=True).click()
        page.wait_for_url("**/#/system")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="系统设置", exact=True)).to_be_visible()
        page.get_by_role("tab", name="通知", exact=True).click()
        expect(page.get_by_role("heading", name="通用 Webhook", exact=True)).to_be_visible()

        page.goto(f"{BASE_URL}/web/console/#/fingerprints")
        page.wait_for_url("**/#/models")
        expect(page.get_by_role("heading", name="模型与测试", exact=True)).to_be_visible()

        desktop_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=desktop_screenshot, type="jpeg", quality=85)

        page.set_viewport_size({"width": 1024, "height": 768})
        page.goto(f"{BASE_URL}/web/console/#/accounts")
        page.wait_for_load_state("networkidle")
        page.wait_for_timeout(250)
        compact_page_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        account_records = page.locator(".account-records")
        table_horizontal_scroll = None
        if account_records.count():
            table_horizontal_scroll = account_records.evaluate(
                "element => element.scrollWidth > element.clientWidth + 1"
            )
        page.screenshot(path=compact_accounts_screenshot, type="jpeg", quality=85)

        page.set_viewport_size({"width": 390, "height": 844})
        page.goto(f"{BASE_URL}/web/console/#/checkins")
        page.wait_for_load_state("networkidle")
        page.wait_for_timeout(250)
        mobile_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=mobile_screenshot, type="jpeg", quality=85)
        browser.close()

    result = {
        "desktop_screenshot": desktop_screenshot,
        "mobile_screenshot": mobile_screenshot,
        "account_dialog_screenshot": account_dialog_screenshot,
        "accounts_page_screenshot": accounts_page_screenshot,
        "compact_accounts_screenshot": compact_accounts_screenshot,
        "credential_dialog_screenshot": credential_dialog_screenshot,
        "sync_dialog_screenshot": sync_dialog_screenshot,
        "desktop_overflow": desktop_overflow,
        "compact_page_overflow": compact_page_overflow,
        "table_horizontal_scroll": table_horizontal_scroll,
        "mobile_overflow": mobile_overflow,
        "console_errors": console_errors,
        "failed_responses": failed_responses,
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if desktop_overflow or compact_page_overflow or table_horizontal_scroll is False or mobile_overflow or console_errors or failed_responses:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
