from pathlib import Path
import tempfile

from playwright.sync_api import sync_playwright


ROOT = Path(__file__).resolve().parents[1]


def check_page(page, width: int, height: int) -> None:
    page.set_viewport_size({"width": width, "height": height})
    page.goto((ROOT / "www" / "index.html").as_uri(), wait_until="networkidle")
    assert page.title() == "PivotFlow · 枢衡"
    assert page.locator("h1").inner_text().startswith("一个界面管理站点")
    assert page.locator("img").count() >= 4
    assert page.locator("img").evaluate_all("imgs => imgs.every(img => img.complete && img.naturalWidth > 0)")
    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
    page.screenshot(path=str(Path(tempfile.gettempdir()) / f"pivotflow-www-{width}.png"), full_page=True)


def main() -> None:
    console_errors = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page()
        page.on("console", lambda message: console_errors.append(message.text) if message.type == "error" else None)
        check_page(page, 1440, 1000)
        check_page(page, 390, 844)
        browser.close()
    assert not console_errors, console_errors
    print("Verified www/index.html at 1440x1000 and 390x844")


if __name__ == "__main__":
    main()
