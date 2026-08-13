import os
import tempfile

from playwright.sync_api import expect, sync_playwright


BASE_URL = os.environ.get("CCLOAD_SMOKE_URL", "http://127.0.0.1:8087")
PASSWORD = os.environ.get("CCLOAD_SMOKE_PASSWORD", "")
ARTIFACT_DIR = os.path.join(tempfile.gettempdir(), "ccload-console-ui")


def main():
    if not PASSWORD:
        raise RuntimeError("CCLOAD_SMOKE_PASSWORD must be set")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        errors = []
        page.on("console", lambda message: errors.append(message.text) if message.type == "error" else None)
        page.goto(f"{BASE_URL}/web/login.html")
        page.wait_for_load_state("networkidle")
        page.locator("#password").fill(PASSWORD)
        page.locator("#login-button").click()
        page.wait_for_url("**/web/console/")
        page.wait_for_load_state("networkidle")

        page.goto(f"{BASE_URL}/web/console/#/sites")
        page.wait_for_load_state("networkidle")
        page.get_by_role("button", name="添加站点", exact=True).click()
        dialog = page.get_by_role("dialog", name="添加站点")
        expect(dialog).to_be_visible()
        dialog.screenshot(path=os.path.join(ARTIFACT_DIR, "console-site-create.png"))
        page.get_by_role("button", name="关闭弹窗").click()

        page.goto(f"{BASE_URL}/web/console/#/channels")
        page.wait_for_load_state("networkidle")
        page.get_by_role("button", name="添加渠道", exact=True).click()
        dialog = page.get_by_role("dialog", name="添加渠道")
        expect(dialog).to_be_visible()
        dialog.screenshot(path=os.path.join(ARTIFACT_DIR, "console-channel-create.png"))
        page.get_by_role("button", name="关闭弹窗").click()

        page.goto(f"{BASE_URL}/web/console/#/tokens")
        page.wait_for_load_state("networkidle")
        page.get_by_role("button", name="创建密钥", exact=True).click()
        dialog = page.get_by_role("dialog", name="创建下游密钥")
        expect(dialog).to_be_visible()
        dialog.screenshot(path=os.path.join(ARTIFACT_DIR, "console-token-create.png"))
        desktop_overflow = page.evaluate("document.documentElement.scrollWidth > document.documentElement.clientWidth + 1")

        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(200)
        mobile_overflow = page.evaluate("document.documentElement.scrollWidth > document.documentElement.clientWidth + 1")
        page.screenshot(path=os.path.join(ARTIFACT_DIR, "console-token-create-mobile.png"), full_page=True)
        browser.close()

    print({"desktop_overflow": desktop_overflow, "mobile_overflow": mobile_overflow, "console_errors": errors})
    if desktop_overflow or mobile_overflow or errors:
        raise SystemExit("UI review failed")


if __name__ == "__main__":
    main()
