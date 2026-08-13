import json
import os
import tempfile

from playwright.sync_api import expect, sync_playwright


BASE_URL = os.environ.get("PIVOTFLOW_SMOKE_URL", "http://127.0.0.1:8080")
PASSWORD = os.environ.get("PIVOTFLOW_SMOKE_PASSWORD")


def main():
    if not PASSWORD:
        raise SystemExit("PIVOTFLOW_SMOKE_PASSWORD is required")
    artifact_dir = os.path.join(tempfile.gettempdir(), "pivotflow-sites-ui")
    os.makedirs(artifact_dir, exist_ok=True)
    desktop_path = os.path.join(artifact_dir, "sites-desktop.png")
    mobile_path = os.path.join(artifact_dir, "sites-mobile.png")
    console_errors = []
    failed_responses = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda message: console_errors.append(message.text) if message.type == "error" else None)
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

        page.goto(f"{BASE_URL}/web/sites.html")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="站点管理", exact=True)).to_be_visible()
        expect(page.locator(".site-card")).to_have_count(2)
        expect(page.locator('a[data-nav-key="sites"]')).to_have_class("topnav-link active")

        page.locator("#btn-add-site").click()
        expect(page.locator("#site-modal")).to_have_attribute("aria-hidden", "false")
        page.locator('#site-modal [data-action="close-site-modal"]').last.click()
        expect(page.locator("#site-modal")).to_have_attribute("aria-hidden", "true")

        page.locator("#site-search").fill("备用")
        expect(page.locator(".site-card")).to_have_count(1)
        page.locator("#site-search").fill("")
        expect(page.locator(".site-card")).to_have_count(2)

        page.locator('.sites-tab[data-tab="announcements"]').click()
        expect(page.locator("#tab-announcements")).to_be_visible()
        page.locator('.sites-tab[data-tab="models"]').click()
        expect(page.locator("#tab-models")).to_be_visible()
        page.locator('.sites-tab[data-tab="sites"]').click()

        page.screenshot(path=desktop_path, full_page=True)
        desktop_overflow = page.evaluate("document.documentElement.scrollWidth > document.documentElement.clientWidth + 1")

        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(250)
        expect(page.locator(".site-card")).to_have_count(2)
        mobile_overflow = page.evaluate("document.documentElement.scrollWidth > document.documentElement.clientWidth + 1")
        page.screenshot(path=mobile_path, full_page=True)
        browser.close()

    result = {
        "desktop_screenshot": desktop_path,
        "mobile_screenshot": mobile_path,
        "desktop_overflow": desktop_overflow,
        "mobile_overflow": mobile_overflow,
        "console_errors": console_errors,
        "failed_responses": failed_responses,
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if desktop_overflow or mobile_overflow or console_errors or failed_responses:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
