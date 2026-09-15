"""Audit console-wide visual rhythm, iconography, color, and layout health."""

import argparse
import json
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

from analytics_ui_smoke import AnalyticsFixtures
from console_visual_regression import QuietHandler


ROOT = Path(__file__).resolve().parents[1]
NOW = 1_786_579_200_000

ANNOUNCEMENTS = [
    {
        "id": 1,
        "site_id": 1,
        "source_key": "maintenance",
        "title": "9 月 16 日凌晨例行维护",
        "content_markdown": "维护期间接口可能出现短暂重试，建议保持自动故障转移开启。",
        "level": "important",
        "source_url": "https://example.net/announcement/1",
        "first_seen_at": NOW - 3_600_000,
        "last_seen_at": NOW - 1_800_000,
    },
    {
        "id": 2,
        "site_id": 2,
        "source_key": "model-update",
        "title": "新增 Claude Sonnet 4.6 兼容路由",
        "content_markdown": "新模型已经进入灰度，模型清单刷新后可直接测试。",
        "level": "normal",
        "source_url": "https://example.net/announcement/2",
        "first_seen_at": NOW - 86_400_000,
        "last_seen_at": NOW - 43_200_000,
        "read_at": NOW - 21_600_000,
    },
]


class DesignAuditFixtures(AnalyticsFixtures):
    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/announcements":
            route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps({"success": True, "data": ANNOUNCEMENTS, "count": len(ANNOUNCEMENTS)}, ensure_ascii=False),
            )
            return
        super().route(route)


ROUTES = [
    ("dashboard", "/"),
    ("sites", "/sites"),
    ("accounts", "/accounts"),
    ("checkins", "/checkins"),
    ("channels", "/channels"),
    ("tokens", "/tokens"),
    ("logs", "/logs"),
    ("stats", "/stats"),
    ("models", "/models"),
    ("trend", "/trend"),
    ("announcements", "/announcements"),
    ("settings", "/system"),
]


AUDIT_SCRIPT = r"""
() => {
  const visible = (element) => {
    const box = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return box.width > 0 && box.height > 0 && style.visibility !== 'hidden' && style.display !== 'none';
  };
  const color = (element) => getComputedStyle(element).color;
  const iconInventory = Array.from(document.querySelectorAll('svg'))
    .filter(visible)
    .map((element) => {
      const box = element.getBoundingClientRect();
      return `${Math.round(box.width)}x${Math.round(box.height)} ${color(element)}`;
    })
    .reduce((counts, key) => counts.set(key, (counts.get(key) || 0) + 1), new Map());
  const buttonInventory = Array.from(document.querySelectorAll('button, a.primary-button, a.secondary-button'))
    .filter(visible)
    .map((element) => {
      const style = getComputedStyle(element);
      return `${style.backgroundColor} ${style.color} ${style.borderRadius}`;
    })
    .reduce((counts, key) => counts.set(key, (counts.get(key) || 0) + 1), new Map());
  const centeredIconIssues = Array.from(document.querySelectorAll('.page-header-icon, .metric-icon, .panel-title-icon, .model-vendor'))
    .filter(visible)
    .flatMap((container) => {
      const icon = container.querySelector('svg');
      if (!icon || !visible(icon)) return [];
      const containerBox = container.getBoundingClientRect();
      const iconBox = icon.getBoundingClientRect();
      const dx = iconBox.x + iconBox.width / 2 - containerBox.x - containerBox.width / 2;
      const dy = iconBox.y + iconBox.height / 2 - containerBox.y - containerBox.height / 2;
      return Math.abs(dx) > 0.75 || Math.abs(dy) > 0.75 ? [{ selector: container.className, dx, dy }] : [];
    });
  const smallText = Array.from(document.querySelectorAll('body *'))
    .filter(visible)
    .filter((element) => element.childNodes.length && Array.from(element.childNodes).some((node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim()))
    .map((element) => ({ text: element.textContent.trim().slice(0, 40), size: Number.parseFloat(getComputedStyle(element).fontSize) }))
    .filter((item) => item.size < 11);
  // The page header intentionally lets its decorative radial pseudo-element bleed
  // outside the clipped box, so its scrollWidth is not a content overflow signal.
  const overflow = Array.from(document.querySelectorAll('.header-controls, .filter-bar, .record-head, .record-row, .row-actions, .account-actions, .checkin-actions, .model-card, .model-card > header, .stats-distribution-list > div, .trend-breakdown-list > div'))
    .filter(visible)
    .map((element) => ({ selector: element.className, text: element.textContent.trim().slice(0, 60), clientWidth: element.clientWidth, scrollWidth: element.scrollWidth }))
    .filter((item) => item.scrollWidth > item.clientWidth + 1);
  const headerIcon = document.querySelector('.page-header-icon');
  const headerIconSvg = headerIcon?.querySelector('svg');
  const activeNav = document.querySelector('.nav-item--active');
  const samplePanel = document.querySelector('.records-panel, .data-panel, .metric-card, .filter-bar, .model-card, .trend-workbench');
  const panelStyle = samplePanel ? getComputedStyle(samplePanel) : null;
  return {
    title: document.querySelector('main h1')?.textContent?.trim() || '',
    viewport: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
    headerIcon: headerIcon ? {
      color: color(headerIcon),
      background: getComputedStyle(headerIcon).backgroundColor,
      borderRadius: getComputedStyle(headerIcon).borderRadius,
      svgSize: headerIconSvg ? `${Math.round(headerIconSvg.getBoundingClientRect().width)}x${Math.round(headerIconSvg.getBoundingClientRect().height)}` : null
    } : null,
    activeNav: activeNav ? {
      color: color(activeNav),
      background: getComputedStyle(activeNav).background,
      borderColor: getComputedStyle(activeNav).borderColor
    } : null,
    samplePanel: panelStyle ? {
      background: panelStyle.background,
      borderColor: panelStyle.borderColor,
      borderRadius: panelStyle.borderRadius,
      boxShadow: panelStyle.boxShadow
    } : null,
    icons: Array.from(iconInventory.entries()).map(([key, count]) => ({ key, count })).sort((a, b) => b.count - a.count).slice(0, 16),
    buttons: Array.from(buttonInventory.entries()).map(([key, count]) => ({ key, count })).sort((a, b) => b.count - a.count).slice(0, 12),
    centeredIconIssues,
    smallText,
    overflow
  };
}
"""


def open_page(browser, base, route, dark):
    context = browser.new_context(viewport={"width": 1510, "height": 950}, color_scheme="dark" if dark else "light", locale="zh-CN", timezone_id="Asia/Shanghai", reduced_motion="reduce")
    context.add_init_script("localStorage.setItem('pivotflow_token','ui-test-only');localStorage.setItem('pivotflow_web_role','admin');")
    data = DesignAuditFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console#{route}", wait_until="networkidle")
    page.wait_for_timeout(250)
    return context, page, errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-ui-design-audit")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    report = {"pages": [], "errors": []}
    captures = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            base = f"http://127.0.0.1:{server.server_port}"
            for dark in (False, True):
                for name, route in ROUTES:
                    context, page, errors = open_page(browser, base, route, dark)
                    try:
                        record = page.evaluate(AUDIT_SCRIPT)
                        record["route"] = name
                        record["theme"] = "dark" if dark else "light"
                        record["pageErrors"] = errors
                        report["pages"].append(record)
                        report["errors"].extend(f"{name}-{'dark' if dark else 'light'}: {error}" for error in errors)
                        screenshot = args.out / f"{name}-{'dark' if dark else 'light'}.png"
                        page.screenshot(path=str(screenshot), full_page=True, animations="disabled")
                        captures.append(str(screenshot.resolve()))
                    finally:
                        context.close()
            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    problems = {
        "errors": report["errors"],
        "overflow": sum(len(page["overflow"]) for page in report["pages"]),
        "smallText": sum(len(page["smallText"]) for page in report["pages"]),
        "centeredIconIssues": sum(len(page["centeredIconIssues"]) for page in report["pages"]),
        "toneMismatch": sum(page.get("headerIcon", {}).get("color") != page.get("activeNav", {}).get("color") for page in report["pages"]),
    }
    print(json.dumps({"passed": not any(problems.values()), "pages": len(report["pages"]), "captures": len(captures), "problems": problems, "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
