import json
import os
import re
import sys
import tempfile

from playwright.sync_api import expect, sync_playwright


BASE_URL = os.environ.get("PIVOTFLOW_SMOKE_URL", "http://127.0.0.1:8080")
PASSWORD = os.environ.get("PIVOTFLOW_SMOKE_PASSWORD")

# 体积预算分两级：全站预取总量 + 单页 chunk。
#
# 控制台在挂载 600ms 后主动预取**全部**路由 chunk（console/src/App.tsx:211-221，
# 为的是首次点击导航不出现载入闪烁），因此「按路由首次加载的资源数」恒为 0，
# 衡量不出东西。用户真正付出的是一次会话的总下载量，所以总量仍是硬门禁；
# 单页 chunk 体积是**归因**维度，回答「总量涨了，是哪个页面胖了」。
MAX_TOTAL_TRANSFER_BYTES = 400_000
MAX_ROUTE_TRANSFER_BYTES = 150_000
MAX_ROUTE_DECODED_BYTES = 450_000
# 分块后每页各自成 chunk，旧值 8（单打包时代）已失效，只作「chunk 数量失控」的兜底。
MAX_CONSOLE_RESOURCES = 45
# 单个页面 chunk 占全站预取总量超过此比例就打警告（**不判失败**）：
# 意味着一个页面的依赖链压过了其余所有页面之和，值得单独看一眼。
ROUTE_SHARE_WARN_RATIO = 0.25

# 页面 chunk 文件名前缀 -> 路由。vite 按组件名生成 `Component-<hash>.js`，
# 组件名见 console/src/App.tsx 的 lazy 导入。壳（index / css / shared / 图标）
# 不归属任何路由，只计入总量——它们每个会话都要下，属固定成本。
PAGE_CHUNK_ROUTES = {
    "DashboardPage": "#/",
    "ChannelsPage": "#/channels",
    "LogsPage": "#/logs",
    "StatsPage": "#/stats",
    "ModelTestPage": "#/models",
    "SitesPage": "#/sites",
    "AccountsPageV2": "#/accounts",
    "CheckinsPage": "#/checkins",
    "AnnouncementsPage": "#/announcements",
    "TokensPage": "#/tokens",
    "TrendPage": "#/trend",
    "SystemSettingsPageV2": "#/system",
}

# 页面 chunk 的命名形态：vite 按组件名生成，PascalCase 且以 Page / PageV2 结尾。
# 这种名字出现在 unattributed 里，就说明有页面没登记进 PAGE_CHUNK_ROUTES ——
# 它的体积会被悄悄排除在单页门禁之外，所以必须报出来。
UNMAPPED_PAGE_CHUNK = re.compile(r"^[A-Z][A-Za-z0-9]*Page(?:V2)?(?:-|$)")


def attribute_page_chunks(resources):
    """把已加载的资源按页面 chunk 前缀归因到路由。

    返回 (by_route, unattributed)：
    - by_route 按 transfer 降序，每项含 route / transfer_bytes / decoded_bytes / count / share
    - unattributed 是壳（index / css / 共享模块 / 图标）的合计——它不归属任何路由，
      但每个会话都要下，属固定成本，所以只计入总量分母。

    share 以「全部 console 资源」为分母，所以各路由 share 之和小于 1。

    unattributed 带 names，是为了让「新增页面忘了登记到 PAGE_CHUNK_ROUTES」可见：
    漏登记的页面 chunk 会静静落进这里，单页门禁就再也看不到它了。
    """
    by_route = {}
    unattributed = {"transfer_bytes": 0, "decoded_bytes": 0, "count": 0, "names": []}
    for item in resources:
        # vite 生产构建把 chunk 命名为 `Component-<hash>.js`（见 PAGE_CHUNK_ROUTES 注释）；
        # 去掉扩展名后按 `<前缀>-` 精确切边界，避免 SitesPage 误吃 SitesPageV2 之类的前缀重叠。
        stem = item["name"].rsplit(".", 1)[0]
        route = next(
            (
                mapped
                for prefix, mapped in PAGE_CHUNK_ROUTES.items()
                if stem == prefix or stem.startswith(f"{prefix}-")
            ),
            None,
        )
        if route is None:
            target = unattributed
        else:
            target = by_route.setdefault(
                route, {"route": route, "transfer_bytes": 0, "decoded_bytes": 0, "count": 0}
            )
        target["transfer_bytes"] += item["transfer_size"]
        target["decoded_bytes"] += item["decoded_size"]
        target["count"] += 1
        if route is None:
            unattributed["names"].append(item["name"])
    total = sum(bucket["transfer_bytes"] for bucket in by_route.values())
    total += unattributed["transfer_bytes"]
    ordered = sorted(by_route.values(), key=lambda bucket: bucket["transfer_bytes"], reverse=True)
    for bucket in ordered:
        bucket["share"] = round(bucket["transfer_bytes"] / total, 4) if total else 0.0
    return ordered, unattributed


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

        expect(page.get_by_role("heading", name="系统概览", exact=True)).to_be_visible()
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

        page_chunks, unattributed_chunks = attribute_page_chunks(performance["resources"])

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

        page.get_by_role("tab", name="导入导出", exact=True).click()
        expect(page.get_by_role("heading", name="导出配置", exact=True)).to_be_visible()
        expect(page.get_by_role("heading", name="从文件导入", exact=True)).to_be_visible()
        expect(page.get_by_role("heading", name="WebDAV 备份", exact=True)).to_be_visible()
        expect(page.locator(".backup-type-card strong", has_text="完整备份")).to_be_visible()
        expect(page.get_by_placeholder("https://dav.example.com/PivotFlow")).to_be_visible()
        backup_overflow = page.evaluate(
            "document.documentElement.scrollWidth > document.documentElement.clientWidth + 1"
        )
        page.screenshot(path=system_path, full_page=True)

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="模型测试", exact=True).click()
        page.wait_for_url("**/#/models")
        expect(page.get_by_role("tab", name="模型清单")).to_have_attribute("aria-selected", "true")
        page.get_by_role("tab", name="连通测试").click()
        expect(page.get_by_role("button", name="站点账号直测", exact=True)).to_be_visible()
        expect(page.get_by_role("button", name="路由渠道", exact=True)).to_be_visible()

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="站点管理", exact=True).click()
        page.wait_for_url("**/#/sites")
        expect(page.locator(".pagination select")).to_have_value("50")
        expect(page.locator(".pagination select option")).to_have_count(2)
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
        expect(page.locator(".pagination select")).to_have_value("50")
        expect(page.locator(".pagination select option")).to_have_text(["50", "100"])

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="渠道分发", exact=True).click()
        expect(page.locator(".pagination select")).to_have_value("50")
        expect(page.locator(".pagination select option")).to_have_text(["50", "100"])
        page.get_by_role("button", name="其他来源", exact=True).click()
        page.get_by_role("menuitem", name="手工渠道", exact=True).click()
        expect(page.get_by_role("dialog", name="添加渠道")).to_be_visible()
        expect(page.get_by_text("渠道模型", exact=True)).to_be_visible()
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
        page.get_by_role("tab", name="导入导出", exact=True).click()
        expect(page.get_by_role("heading", name="WebDAV 备份", exact=True)).to_be_visible()
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
        "backup_overflow": backup_overflow,
        "route_checks": route_checks,
        "console_errors": console_errors,
        "failed_responses": failed_responses,
        "performance": performance,
        "page_chunks": {
            "by_route": page_chunks,
            "unattributed": unattributed_chunks,
            "largest_route": page_chunks[0] if page_chunks else None,
        },
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))

    failures = []
    if desktop_overflow or mobile_overflow or settings_mobile_overflow or backup_overflow:
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
        failures.append(
            f"console resources exceed {MAX_CONSOLE_RESOURCES}: {performance['resource_count']}"
        )
    # 控制台预取全部路由 chunk，所以「一次会话实际要下多少」就是总量，这是硬门禁。
    total_transfer = performance["static_transfer_bytes"] or performance["static_decoded_bytes"]
    if total_transfer > MAX_TOTAL_TRANSFER_BYTES:
        failures.append(
            f"console prefetch total exceeds {MAX_TOTAL_TRANSFER_BYTES} bytes: {total_transfer}"
        )
    # 归因维度：总量涨了要能回答「哪个页面胖了」，所以单页 chunk 也有硬上限。
    # 传输与解压分开取最大值——最胖的可能是两个不同的页面。
    heaviest_transfer = max(page_chunks, key=lambda b: b["transfer_bytes"], default=None)
    if heaviest_transfer and heaviest_transfer["transfer_bytes"] > MAX_ROUTE_TRANSFER_BYTES:
        failures.append(
            f"route {heaviest_transfer['route']} transfer exceeds {MAX_ROUTE_TRANSFER_BYTES} bytes: "
            f"{heaviest_transfer['transfer_bytes']}"
        )
    heaviest_decoded = max(page_chunks, key=lambda b: b["decoded_bytes"], default=None)
    if heaviest_decoded and heaviest_decoded["decoded_bytes"] > MAX_ROUTE_DECODED_BYTES:
        failures.append(
            f"route {heaviest_decoded['route']} decoded exceeds {MAX_ROUTE_DECODED_BYTES} bytes: "
            f"{heaviest_decoded['decoded_bytes']}"
        )
    # 漏登记的页面 chunk 会让单页门禁出现盲区，报出来（同样不判失败）。
    for name in unattributed_chunks["names"]:
        if UNMAPPED_PAGE_CHUNK.match(name.rsplit(".", 1)[0]):
            print(
                f"[warn] page chunk {name} is not in PAGE_CHUNK_ROUTES — its weight is not "
                "measured per route",
                file=sys.stderr,
            )
    # 占比告警**不判失败**：一个页面的依赖链压过其余所有页面之和，值得看一眼，
    # 但还不到拦下的程度——硬拦会逼着后来者去调阈值，反而把信号磨掉。
    for bucket in page_chunks:
        if bucket["share"] > ROUTE_SHARE_WARN_RATIO:
            print(
                f"[warn] route {bucket['route']} is {bucket['share']:.0%} of console prefetch "
                f"({bucket['transfer_bytes']} / {total_transfer} bytes)",
                file=sys.stderr,
            )
    if failures:
        raise SystemExit("; ".join(failures))


if __name__ == "__main__":
    main()
