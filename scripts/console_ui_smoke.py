import json
import os
import tempfile

from playwright.sync_api import expect, sync_playwright


BASE_URL = os.environ.get("PIVOTFLOW_SMOKE_URL", "http://127.0.0.1:8080")
PASSWORD = os.environ.get("PIVOTFLOW_SMOKE_PASSWORD")
MAX_STATIC_BYTES = 350_000
MAX_CONSOLE_RESOURCES = 8


def main():
    if not PASSWORD:
        raise SystemExit("PIVOTFLOW_SMOKE_PASSWORD is required")

    artifact_dir = os.path.join(tempfile.gettempdir(), "pivotflow-console-ui")
    os.makedirs(artifact_dir, exist_ok=True)
    desktop_path = os.path.join(artifact_dir, "console-desktop.png")
    mobile_path = os.path.join(artifact_dir, "console-mobile.png")
    mobile_drawer_path = os.path.join(artifact_dir, "console-mobile-drawer.png")
    channels_path = os.path.join(artifact_dir, "console-channels.png")
    logs_path = os.path.join(artifact_dir, "console-logs.png")
    stats_path = os.path.join(artifact_dir, "console-stats.png")
    model_test_path = os.path.join(artifact_dir, "console-model-test.png")
    advanced_path = os.path.join(artifact_dir, "console-advanced.png")
    settings_mobile_path = os.path.join(artifact_dir, "console-settings-mobile.png")
    tokens_path = os.path.join(artifact_dir, "console-tokens.png")
    trend_path = os.path.join(artifact_dir, "console-trend.png")
    fingerprints_path = os.path.join(artifact_dir, "console-fingerprints.png")
    system_path = os.path.join(artifact_dir, "console-system.png")
    search_path = os.path.join(artifact_dir, "console-global-search.png")
    sites_path = os.path.join(artifact_dir, "console-sites.png")
    accounts_path = os.path.join(artifact_dir, "console-accounts.png")
    checkins_path = os.path.join(artifact_dir, "console-checkins.png")
    announcements_path = os.path.join(artifact_dir, "console-announcements.png")
    console_errors = []
    failed_responses = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900}, device_scale_factor=1)
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

        page.goto(f"{BASE_URL}/web/auth/")
        page.wait_for_load_state("networkidle")
        page.locator("#password").fill(PASSWORD)
        page.locator("#login-button").click()
        page.wait_for_url("**/web/console/")
        page.wait_for_load_state("networkidle")

        expect(page.get_by_role("heading", name="概览", exact=True)).to_be_visible()
        expect(page.locator(".metric-card")).to_have_count(4)
        expect(page.locator(".tool-card")).to_have_count(4)
        expect(page.locator(".sidebar")).to_be_visible()

        page.get_by_role("button", name="全局搜索", exact=True).click()
        search_dialog = page.get_by_role("dialog", name="全局搜索")
        expect(search_dialog).to_be_visible()
        search_dialog.get_by_role("textbox", name="全局搜索内容").fill("OAuth")
        expect(search_dialog.get_by_role("link", name="OAuth 凭证导入")).to_be_visible()
        page.screenshot(path=search_path)
        search_dialog.get_by_role("button", name="关闭全局搜索").click()
        expect(search_dialog).not_to_be_visible()

        with page.expect_response(lambda response: "/admin/dashboard?range=this_week" in response.url) as response_info:
            page.get_by_role("radio", name="本周").click()
        if response_info.value.status != 200:
            raise AssertionError(f"week dashboard status={response_info.value.status}")
        expect(page.get_by_role("radio", name="本周")).to_have_attribute("aria-checked", "true")

        desktop_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=desktop_path, full_page=True)

        performance = page.evaluate(
            """
            () => {
              const entries = performance.getEntriesByType('resource')
                .filter((entry) => entry.name.includes('/web/console/'));
              const navigation = performance.getEntriesByType('navigation')[0];
              return {
                resource_count: entries.length,
                static_transfer_bytes: entries.reduce((sum, entry) => sum + (entry.transferSize || 0), 0),
                static_decoded_bytes: entries.reduce((sum, entry) => sum + (entry.decodedBodySize || 0), 0),
                dom_content_loaded_ms: navigation ? navigation.domContentLoadedEventEnd : null,
                load_ms: navigation ? navigation.loadEventEnd : null,
                resources: entries.map((entry) => ({
                  name: entry.name.split('/').pop(),
                  transfer_size: entry.transferSize || 0,
                  decoded_size: entry.decodedBodySize || 0,
                  duration_ms: Math.round(entry.duration),
                })),
              };
            }
            """
        )

        route_checks = []
        console_routes = [
            ("站点管理", "站点管理", "#/sites", sites_path),
            ("账号管理", "账号管理", "#/accounts", accounts_path),
            ("签到中心", "签到中心", "#/checkins", checkins_path),
            ("公告中心", "公告中心", "#/announcements", announcements_path),
            ("渠道分发", "渠道分发", "#/channels", channels_path),
            ("请求日志", "请求日志", "#/logs", logs_path),
            ("用量统计", "用量统计", "#/stats", stats_path),
            ("消费趋势", "消费趋势", "#/trend", trend_path),
            ("模型测试", "模型测试", "#/models", model_test_path),
            ("令牌管理", "令牌管理", "#/tokens", tokens_path),
            ("系统设置", "系统设置", "#/system", system_path),
        ]
        for nav_label, heading, route, screenshot_path in console_routes:
            page.get_by_role("navigation", name="主导航").get_by_role("link", name=nav_label, exact=True).click()
            page.wait_for_url(f"**/{route}")
            page.wait_for_load_state("networkidle")
            page.wait_for_timeout(300)
            expect(page.get_by_role("heading", name=heading, exact=True)).to_be_visible()
            expect(page.locator(f'a.nav-item[href="{route}"]')).to_have_class(
                "nav-item nav-item--active"
            )
            overflow = page.evaluate(
                "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
            )
            legacy_links = page.locator('a[href^="/web/"]').count()
            route_checks.append({"route": route, "overflow": overflow, "legacy_links": legacy_links})
            page.screenshot(path=screenshot_path, full_page=True)

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="模型测试", exact=True).click()
        page.wait_for_url("**/#/models")
        expect(page.get_by_role("tab", name="模型清单")).to_have_attribute("aria-selected", "true")
        page.get_by_role("tab", name="连通测试").click()
        expect(page.get_by_role("button", name="站点账号直测", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="路由渠道", exact=True)).to_be_visible()

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="站点管理", exact=True).click()
        page.wait_for_url("**/#/sites")
        expect(page.locator(".pagination select")).to_have_value("20")
        expect(page.locator(".pagination select option")).to_have_count(3)
        page.get_by_role("button", name="添加站点", exact=True).click()
        site_dialog = page.get_by_role("dialog", name="添加站点")
        expect(site_dialog).to_be_visible()
        expect(site_dialog.get_by_text("同时添加首个账号", exact=True)).to_be_visible()
        expect(site_dialog.get_by_label("平台类型")).to_contain_text("One API")
        site_dialog.get_by_label("平台类型").select_option("sub2api")
        site_dialog.get_by_label("添加方式").select_option("access_token")
        expect(site_dialog.get_by_text("Refresh Token（可选）", exact=True)).to_be_visible()
        expect(site_dialog.get_by_text("访问令牌过期时间（可选）", exact=True)).to_be_visible()
        page.get_by_role("button", name="关闭弹窗").click()
        expect(page.get_by_role("dialog", name="添加站点")).not_to_be_visible()

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="账号管理", exact=True).click()
        page.wait_for_url("**/#/accounts")
        expect(page.locator(".pagination select")).to_have_value("20")
        expect(page.locator(".pagination select option")).to_have_text(["20", "50", "100"])

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="渠道分发", exact=True).click()
        expect(page.locator(".pagination select")).to_have_value("20")
        expect(page.locator(".pagination select option")).to_have_text(["20", "50", "100"])
        page.get_by_role("button", name="其他来源", exact=True).click()
        page.get_by_role("menuitem", name="手工渠道", exact=True).click()
        expect(page.get_by_role("dialog", name="添加渠道")).to_be_visible()
        expect(page.get_by_text("模型映射", exact=True)).to_be_visible()
        page.get_by_role("button", name="关闭弹窗").click()

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="令牌管理", exact=True).click()
        page.get_by_role("button", name="创建令牌", exact=True).click()
        expect(page.get_by_role("dialog", name="创建令牌")).to_be_visible()
        expect(page.get_by_text("允许的模型", exact=True)).to_be_visible()
        page.get_by_role("button", name="关闭弹窗").click()

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="请求日志", exact=True).click()
        status_width = page.evaluate(
            """
            () => {
              const node = document.createElement('div');
              node.className = 'record-head log-grid';
              node.innerHTML = '<span></span><span></span><span></span><span></span><span></span><span></span><span></span>';
              node.style.width = '1000px';
              document.body.appendChild(node);
              const width = node.children[3].getBoundingClientRect().width;
              node.remove();
              return width;
            }
            """
        )
        if status_width < 120:
            raise AssertionError(f"log status column too narrow: {status_width}")
        page.get_by_role("tab", name="进行中", exact=True).click()
        page.wait_for_url("**/#/logs?view=active")
        expect(page.get_by_text("当前没有进行中的请求", exact=True)).to_be_visible()

        toast_style = page.evaluate(
            """
            () => {
              const node = document.createElement('div');
              node.className = 'operation-notice';
              node.textContent = '操作完成';
              document.body.appendChild(node);
              const style = getComputedStyle(node);
              const result = { position: style.position, top: style.top, right: style.right };
              node.remove();
              return result;
            }
            """
        )
        if toast_style["position"] != "fixed" or toast_style["right"] == "auto":
            raise AssertionError(f"operation notice is not a right-side toast: {toast_style}")

        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(250)
        expect(page.get_by_role("button", name="打开导航")).to_be_visible()
        page.get_by_role("button", name="打开导航").click()
        expect(page.locator(".sidebar")).to_have_class("sidebar sidebar--open")
        expect(page.get_by_text("渠道分发", exact=True)).to_be_visible()
        page.wait_for_timeout(250)
        mobile_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=mobile_drawer_path)
        page.get_by_role("button", name="关闭导航").last.click()
        expect(page.locator(".sidebar")).to_have_class("sidebar")
        page.wait_for_timeout(250)
        page.screenshot(path=mobile_path, full_page=True)
        page.goto(f"{BASE_URL}/web/console/#/system")
        page.wait_for_load_state("networkidle")
        expect(page.get_by_role("heading", name="系统设置", exact=True)).to_be_visible()
        page.wait_for_timeout(300)
        settings_mobile_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=settings_mobile_path, full_page=True)
        browser.close()

    result = {
        "desktop_screenshot": desktop_path,
        "mobile_screenshot": mobile_path,
        "mobile_drawer_screenshot": mobile_drawer_path,
        "channels_screenshot": channels_path,
        "logs_screenshot": logs_path,
        "stats_screenshot": stats_path,
        "model_test_screenshot": model_test_path,
        "advanced_screenshot": advanced_path,
        "settings_mobile_screenshot": settings_mobile_path,
        "search_screenshot": search_path,
        "sites_screenshot": sites_path,
        "accounts_screenshot": accounts_path,
        "checkins_screenshot": checkins_path,
        "announcements_screenshot": announcements_path,
        "desktop_overflow": desktop_overflow,
        "mobile_overflow": mobile_overflow,
        "settings_mobile_overflow": settings_mobile_overflow,
        "route_checks": route_checks,
        "console_errors": console_errors,
        "failed_responses": failed_responses,
        "performance": performance,
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))

    failures = []
    if desktop_overflow or mobile_overflow or settings_mobile_overflow:
        failures.append("horizontal overflow detected")
    if any(check["overflow"] for check in route_checks):
        failures.append("console route overflow detected")
    if any(check["legacy_links"] for check in route_checks):
        failures.append("legacy console links detected")
    if console_errors:
        failures.append("browser console errors detected")
    if failed_responses:
        failures.append("failed network responses detected")
    if performance["resource_count"] > MAX_CONSOLE_RESOURCES:
        failures.append(f"console resources exceed {MAX_CONSOLE_RESOURCES}")
    transfer_size = performance["static_transfer_bytes"] or performance["static_decoded_bytes"]
    if transfer_size > MAX_STATIC_BYTES:
        failures.append(f"console static payload exceeds {MAX_STATIC_BYTES} bytes")
    if failures:
        raise SystemExit("; ".join(failures))


if __name__ == "__main__":
    main()
