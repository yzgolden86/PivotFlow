"""真实打开一次公告详情弹窗，验证「按需加载的 markdown 渲染」没被改坏。

存在的理由：公告详情是**唯一**渲染 markdown 的地方，而 `console_ui_smoke.py` 跑的是
空库 —— 公告列表为空，没有行可点，所以那条路径**一直没有被任何自动化覆盖**。
把 `AnnouncementDetail` 从静态 import 改成 `lazy()` 时，bundle 层能验证
（chunk 没被预取），但组件从没真正渲染过。静态改懒加载恰好是那种
「类型检查过、运行时才炸」的改动（default 导出解析、`Modal` 里的 `Suspense`），
所以这里补上真实渲染。

用法（需要先把服务端指向同一个库起来）：

    # 1) 起服务端（库路径自选）
    SQLITE_PATH=/tmp/pivotflow-detail.db PIVOTFLOW_PASS=<任意> PORT=18082 ./pivotflow &

    # 2) 造数据 + 验证（SEED 必须显式打开，见下面的安全说明）
    PIVOTFLOW_DETAIL_CHECK_URL=http://127.0.0.1:18082 \
    PIVOTFLOW_DETAIL_CHECK_PASSWORD=<同上> \
    PIVOTFLOW_DETAIL_CHECK_DB=/tmp/pivotflow-detail.db \
    PIVOTFLOW_DETAIL_CHECK_SEED=1 \
    python scripts/console_announcement_detail_check.py

安全说明：`PIVOTFLOW_DETAIL_CHECK_SEED` 是**显式开关**，因为写库有风险。开了之后
也**只做 INSERT**（不 DELETE），用的是 id 999999 / 名字 `__detail_check__` 这种不会
和真实数据撞上的取值。**别把线上库路径传给这个脚本。**
"""

import hashlib
import json
import os
import sqlite3
import sys
import time

from playwright.sync_api import expect, sync_playwright

BASE_URL = os.environ.get("PIVOTFLOW_DETAIL_CHECK_URL", "http://127.0.0.1:8080")
PASSWORD = os.environ.get("PIVOTFLOW_DETAIL_CHECK_PASSWORD")
DB_PATH = os.environ.get("PIVOTFLOW_DETAIL_CHECK_DB")
SEED = os.environ.get("PIVOTFLOW_DETAIL_CHECK_SEED") == "1"

CHECK_SITE_ID = 999_999
CHECK_SITE_NAME = "__detail_check__"
CHECK_SITE_BASE_URL = "https://upstream.example.com"
TITLE = "__detail_check__ 渲染与链接"

# 覆盖到每一层渲染管线，缺一层就等于没测：
#   h1 / strong        —— 基础 markdown
#   表格 / 列表         —— remark-gfm 与 remark-breaks
#   站内相对链接        —— resolveAnnouncementContentURL 的 base_url 分支
#   危险链接            —— rehype-sanitize 必须把 javascript: 降级掉
#   原始 HTML           —— rehype-raw 必须解析它而不是转义
CONTENT = """# 上游公告标题

**重要**：这条公告用于验证详情弹窗的 markdown 渲染。

| 项目 | 值 |
| --- | --- |
| 状态 | 正常 |
| 版本 | v1.2.3 |

- 第一项
- 第二项

[站内相对链接](/notice/2)

[外部链接](https://example.com/doc)

[危险链接](javascript:alert(1))

<div class="injected-html">原始 HTML 段落</div>
"""


def seed(db_path):
    """只 INSERT，不 DELETE —— 万一指错库也不至于毁掉真实数据。"""
    now = int(time.time() * 1000)
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    # proxy_url / external_checkin_url 是 nullable，但 Go 侧的扫描器不接受 NULL
    # （会报 converting NULL to string is unsupported，且会让 /admin/sites 直接 500），
    # 所以这里必须显式写空串。
    cur.execute(
        """
        INSERT OR REPLACE INTO sites (id, name, platform, base_url, enabled, timezone,
                                      use_system_proxy, proxy_url, external_checkin_url,
                                      tags_json, last_probe_status, last_error, checkin_method,
                                      checkin_method_checked_at, created_at, updated_at, deleted_at)
        VALUES (?, ?, 'new-api', ?, 1, 'Asia/Shanghai', 0, '', '', '[]', 'unknown', '', '', 0, ?, ?, 0)
        """,
        (CHECK_SITE_ID, CHECK_SITE_NAME, CHECK_SITE_BASE_URL, now, now),
    )
    cur.execute(
        """
        INSERT OR REPLACE INTO site_announcements (site_id, source_key, title, content_markdown,
                                                   level, source_url, upstream_created_at,
                                                   upstream_updated_at, first_seen_at, last_seen_at,
                                                   read_at, content_hash, created_at, updated_at)
        VALUES (?, 'detail-check', ?, ?, 'important', '/api/notice', ?, ?, ?, ?, 0, ?, ?, ?)
        """,
        (
            CHECK_SITE_ID,
            TITLE,
            CONTENT,
            now - 60_000,
            now - 30_000,
            now - 60_000,
            now,
            hashlib.sha256(CONTENT.encode("utf-8")).hexdigest(),
            now,
            now,
        ),
    )
    conn.commit()
    conn.close()
    print(f"seeded {db_path}: site #{CHECK_SITE_ID}, announcement {TITLE!r}", file=sys.stderr)


def main():
    if not PASSWORD:
        raise SystemExit("PIVOTFLOW_DETAIL_CHECK_PASSWORD is required")
    if SEED:
        if not DB_PATH:
            raise SystemExit("PIVOTFLOW_DETAIL_CHECK_SEED=1 requires PIVOTFLOW_DETAIL_CHECK_DB")
        seed(DB_PATH)

    console_errors = []
    failed_responses = []
    detail_chunk_requests = []

    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 900})
        page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
        page.on(
            "response",
            lambda r: failed_responses.append({"status": r.status, "url": r.url})
            if r.status >= 400
            else None,
        )
        page.on(
            "request",
            lambda r: detail_chunk_requests.append(r.url) if "AnnouncementDetail" in r.url else None,
        )

        page.goto(f"{BASE_URL}/web/auth/")
        page.wait_for_load_state("networkidle")
        page.locator("#password").fill(PASSWORD)
        page.locator("#login-button").click()
        page.wait_for_url("**/web/console/")
        page.wait_for_load_state("networkidle")

        # 等过 600ms 的全站预取窗口，确认详情 chunk **没有**被预取带上。
        page.wait_for_timeout(1500)
        prefetched = len(detail_chunk_requests)

        page.get_by_role("navigation", name="主导航").get_by_role("link", name="公告中心", exact=True).click()
        page.wait_for_url("**/#/announcements")
        page.wait_for_load_state("networkidle")

        row = page.locator(".announcement-row", has_text=TITLE).first
        expect(row).to_be_visible()
        row.locator(".announcement-open").click()

        dialog = page.get_by_role("dialog", name=TITLE)
        expect(dialog).to_be_visible()
        md = page.locator(".announcement-markdown")
        expect(md).to_be_visible()
        page.wait_for_timeout(400)
        loaded_on_demand = len(detail_chunk_requests)

        checks = {
            "h1": md.locator("h1").inner_text(),
            "strong": md.locator("strong").first.inner_text(),
            "table_rows": md.locator("table tr").count(),
            "list_items": md.locator("ul li").count(),
            "raw_html_parsed": "<div" not in md.inner_text(),
            "raw_html_text": md.get_by_text("原始 HTML 段落").is_visible(),
            "link_hrefs": md.locator("a").evaluate_all("els => els.map(e => e.getAttribute('href'))"),
            # javascript: 那条应当**不是** <a>，而是退化成 span
            "danger_tag": md.get_by_text("危险链接").evaluate("e => e.tagName"),
        }
        source = page.locator(".announcement-detail-meta a", has_text="查看原文")
        checks["source_href"] = source.get_attribute("href") if source.count() else None

        browser.close()

    report = {
        "prefetched_detail_chunk": prefetched,
        "detail_chunk_requests_after_open": loaded_on_demand,
        "checks": checks,
        "console_errors": console_errors,
        "failed_responses": failed_responses,
    }
    print(json.dumps(report, ensure_ascii=False, indent=2))

    failures = []
    if prefetched != 0:
        failures.append(f"detail chunk was prefetched ({prefetched} request(s))")
    if loaded_on_demand == 0:
        failures.append("detail chunk was never loaded on demand")
    if checks["h1"] != "上游公告标题":
        failures.append(f"h1 mismatch: {checks['h1']!r}")
    if "重要" not in checks["strong"]:
        failures.append(f"strong mismatch: {checks['strong']!r}")
    if checks["table_rows"] != 3:
        failures.append(f"table rows: {checks['table_rows']}")
    if checks["list_items"] != 2:
        failures.append(f"list items: {checks['list_items']}")
    if not checks["raw_html_parsed"] or not checks["raw_html_text"]:
        failures.append("raw HTML was not parsed by rehype-raw")
    expected = {f"{CHECK_SITE_BASE_URL}/notice/2", "https://example.com/doc"}
    missing = expected - set(checks["link_hrefs"])
    if missing:
        failures.append(f"link hrefs missing: {missing}")
    if any("javascript:" in (href or "") for href in checks["link_hrefs"]):
        failures.append("javascript: link survived into an href")
    if checks["danger_tag"] != "SPAN":
        failures.append(f"javascript: link tag is {checks['danger_tag']}, expected SPAN")
    if checks["source_href"] != CHECK_SITE_BASE_URL:
        failures.append(f"查看原文 href: {checks['source_href']!r}")
    if console_errors:
        failures.append(f"console errors: {console_errors}")
    if failed_responses:
        failures.append(f"failed responses: {failed_responses}")
    if failures:
        raise SystemExit("; ".join(failures))


if __name__ == "__main__":
    main()
