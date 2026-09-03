"""Generate sanitized PivotFlow documentation images from the real console UI."""

import json
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import Route, sync_playwright


BASE_URL = "http://127.0.0.1:8089"
OUT = Path("docs/assets")
VIEWPORT = {"width": 1600, "height": 1000}

NOW = 1_786_579_200_000

SITES = [
    {
        "id": 1,
        "name": "星河节点",
        "platform": "new-api-family",
        "base_url": "https://api.example.net",
        "enabled": True,
        "timezone": "Asia/Shanghai",
        "tags_json": "[]",
        "last_probe_status": "healthy",
        "created_at": NOW - 86_400_000 * 30,
        "updated_at": NOW,
    },
    {
        "id": 2,
        "name": "远岚服务",
        "platform": "sub2api",
        "base_url": "https://relay.example.org",
        "enabled": True,
        "timezone": "Asia/Shanghai",
        "tags_json": "[]",
        "last_probe_status": "healthy",
        "created_at": NOW - 86_400_000 * 18,
        "updated_at": NOW,
    },
    {
        "id": 3,
        "name": "晨光备用",
        "platform": "veloera",
        "base_url": "https://edge.example.com",
        "enabled": True,
        "timezone": "Asia/Shanghai",
        "tags_json": "[]",
        "last_probe_status": "healthy",
        "created_at": NOW - 86_400_000 * 8,
        "updated_at": NOW,
    },
]

ACCOUNTS = {
    1: [
        {
            "id": 11,
            "site_id": 1,
            "label": "主账号",
            "credential_type": "access_token",
            "credential_configured": True,
            "enabled": True,
            "auto_checkin": True,
            "auto_refresh": True,
            "status": "healthy",
            "balance": 82.64,
            "balance_currency": "USD",
            "balance_updated_at": NOW,
            "last_refresh_at": NOW,
            "last_refresh_status": "success",
            "consecutive_failures": 0,
            "last_checkin_at": NOW,
            "last_checkin_status": "success",
            "created_at": NOW - 86_400_000 * 30,
            "updated_at": NOW,
        },
        {
            "id": 12,
            "site_id": 1,
            "label": "高并发组",
            "credential_type": "api_key",
            "credential_configured": True,
            "enabled": True,
            "auto_checkin": False,
            "auto_refresh": True,
            "status": "healthy",
            "balance": 36.18,
            "balance_currency": "USD",
            "balance_updated_at": NOW,
            "last_refresh_at": NOW,
            "last_refresh_status": "success",
            "consecutive_failures": 0,
            "last_checkin_status": "unsupported",
            "created_at": NOW - 86_400_000 * 21,
            "updated_at": NOW,
        },
    ],
    2: [
        {
            "id": 21,
            "site_id": 2,
            "label": "默认订阅",
            "credential_type": "access_token",
            "credential_configured": True,
            "enabled": True,
            "auto_checkin": False,
            "auto_refresh": True,
            "status": "healthy",
            "balance": 54.32,
            "balance_currency": "USD",
            "balance_updated_at": NOW,
            "last_refresh_at": NOW,
            "last_refresh_status": "success",
            "consecutive_failures": 0,
            "last_checkin_status": "unsupported",
            "created_at": NOW - 86_400_000 * 18,
            "updated_at": NOW,
        }
    ],
    3: [
        {
            "id": 31,
            "site_id": 3,
            "label": "容灾账号",
            "credential_type": "cookie",
            "credential_configured": True,
            "enabled": True,
            "auto_checkin": True,
            "auto_refresh": True,
            "status": "healthy",
            "balance": 27.90,
            "balance_currency": "USD",
            "balance_updated_at": NOW,
            "last_refresh_at": NOW,
            "last_refresh_status": "success",
            "consecutive_failures": 0,
            "last_checkin_at": NOW,
            "last_checkin_status": "already_checked",
            "created_at": NOW - 86_400_000 * 8,
            "updated_at": NOW,
        }
    ],
}

CHANNELS = [
    {
        "id": 101,
        "name": "星河主路由",
        "auth_type": "api_key",
        "protocol_transform_mode": "auto",
        "urls": [{"url": "https://api.example.net", "protocols": ["anthropic", "openai"]}],
        "priority": 100,
        "rpm_limit": 120,
        "max_concurrency": 16,
        "enabled": True,
        "models": [{"model": "claude-sonnet-4-6"}, {"model": "gpt-5.4"}, {"model": "gemini-3-pro"}],
        "daily_cost_limit": 35,
        "cost_multiplier": 1,
        "key_count": 3,
        "key_strategy": "round_robin",
        "success_rate": 99.7,
        "websockets": True,
        "retry_other_keys_on_failure": True,
    },
    {
        "id": 102,
        "name": "远岚 Codex",
        "auth_type": "api_key",
        "protocol_transform_mode": "upstream",
        "urls": [{"url": "https://relay.example.org", "protocols": ["codex", "openai"]}],
        "priority": 80,
        "rpm_limit": 80,
        "max_concurrency": 10,
        "enabled": True,
        "models": [{"model": "gpt-5.4-codex"}, {"model": "gpt-5.3-codex"}],
        "daily_cost_limit": 20,
        "cost_multiplier": 0.9,
        "key_count": 2,
        "key_strategy": "sequential",
        "success_rate": 98.9,
        "websockets": True,
        "retry_other_keys_on_failure": True,
    },
    {
        "id": 103,
        "name": "晨光容灾",
        "auth_type": "api_key",
        "protocol_transform_mode": "local",
        "urls": [{"url": "https://edge.example.com", "protocols": ["gemini"]}],
        "priority": 40,
        "rpm_limit": 60,
        "max_concurrency": 6,
        "enabled": True,
        "models": [{"model": "gemini-3-flash"}, {"model": "claude-haiku-4-5"}],
        "daily_cost_limit": 12,
        "cost_multiplier": 1.1,
        "key_count": 1,
        "key_strategy": "sequential",
        "success_rate": 97.8,
        "websockets": False,
        "retry_other_keys_on_failure": False,
    },
]

DASHBOARD = {
    "range": "today",
    "starts_at": NOW - 86_400_000,
    "ends_at": NOW,
    "generated_at": NOW,
    "totals": {
        "requests": 1842,
        "success": 1816,
        "errors": 26,
        "input_tokens": 8_430_000,
        "output_tokens": 1_960_000,
        "cache_read_tokens": 3_210_000,
        "cache_creation_tokens": 720_000,
        "cost": 31.86,
        "effective_cost": 28.42,
    },
    "balances": [{"currency": "USD", "amount": 201.04, "accounts": 4}],
    "model_usage": [
        {"key": "claude-sonnet-4-6", "label": "claude-sonnet-4-6", "requests": 684, "success": 677, "errors": 7, "input_tokens": 3_420_000, "output_tokens": 790_000, "effective_cost": 10.92, "share": 0.384},
        {"key": "gpt-5.4-codex", "label": "gpt-5.4-codex", "requests": 521, "success": 515, "errors": 6, "input_tokens": 2_680_000, "output_tokens": 602_000, "effective_cost": 8.24, "share": 0.290},
        {"key": "gemini-3-pro", "label": "gemini-3-pro", "requests": 376, "success": 369, "errors": 7, "input_tokens": 1_490_000, "output_tokens": 381_000, "effective_cost": 5.86, "share": 0.206},
        {"key": "gpt-5.4", "label": "gpt-5.4", "requests": 261, "success": 255, "errors": 6, "input_tokens": 840_000, "output_tokens": 187_000, "effective_cost": 3.40, "share": 0.120},
    ],
    "site_usage": [
        {"key": "1", "label": "星河节点", "site_id": 1, "requests": 920, "success": 909, "errors": 11, "input_tokens": 4_290_000, "output_tokens": 1_010_000, "effective_cost": 13.52, "share": 0.476},
        {"key": "2", "label": "远岚服务", "site_id": 2, "requests": 576, "success": 568, "errors": 8, "input_tokens": 2_810_000, "output_tokens": 612_000, "effective_cost": 9.24, "share": 0.325},
        {"key": "3", "label": "晨光备用", "site_id": 3, "requests": 346, "success": 339, "errors": 7, "input_tokens": 1_330_000, "output_tokens": 338_000, "effective_cost": 5.66, "share": 0.199},
    ],
    "client_usage": [
        {"key": "anthropic", "label": "Claude Code", "requests": 812, "success": 803, "errors": 9, "input_tokens": 3_760_000, "output_tokens": 884_000, "effective_cost": 11.18, "share": 0.393},
        {"key": "codex", "label": "Codex", "requests": 498, "success": 491, "errors": 7, "input_tokens": 2_510_000, "output_tokens": 572_000, "effective_cost": 7.94, "share": 0.279},
        {"key": "gemini", "label": "Gemini", "requests": 321, "success": 316, "errors": 5, "input_tokens": 1_310_000, "output_tokens": 320_000, "effective_cost": 5.31, "share": 0.187},
        {"key": "openai", "label": "OpenAI", "requests": 211, "success": 206, "errors": 5, "input_tokens": 850_000, "output_tokens": 184_000, "effective_cost": 3.99, "share": 0.141},
    ],
    "trend": [
        {"ts": f"2026-08-13T{str(i).zfill(2)}:00:00+08:00", "success": 48 + (i * 13) % 54, "error": (i * 3) % 4, "effective_cost": round(0.42 + (i * 0.17) % 1.45, 2), "input_tokens": 210_000 + i * 9_000, "output_tokens": 42_000 + i * 3_000}
        for i in range(24)
    ],
    "unread_notices": 2,
    "site_count": 3,
    "enabled_sites": 3,
    "account_count": 4,
    "healthy_accounts": 4,
    "channel_count": 3,
    "enabled_channels": 3,
}


def envelope(data, count=None):
    payload = {"success": True, "data": data}
    if count is not None:
        payload["count"] = count
    return payload


def mock_admin(route: Route):
    parsed = urlparse(route.request.url)
    path = parsed.path
    if path == "/dashboard/session":
        payload = {"success": True, "data": {"role": "admin"}}
    elif path == "/admin/dashboard":
        payload = envelope(DASHBOARD)
    elif path == "/admin/site-inventory":
        all_accounts = []
        for accounts in ACCOUNTS.values():
            all_accounts.extend(accounts)
        payload = envelope({"sites": SITES, "accounts": all_accounts})
    elif path == "/admin/verify":
        payload = {"success": True, "authenticated": True}
    elif path == "/admin/sites":
        payload = envelope(SITES, len(SITES))
    elif path.startswith("/admin/sites/") and path.endswith("/accounts"):
        site_id = int(path.split("/")[3])
        payload = envelope(ACCOUNTS.get(site_id, []), len(ACCOUNTS.get(site_id, [])))
    elif path == "/admin/channels":
        payload = envelope(CHANNELS, len(CHANNELS))
    elif path == "/admin/site-channel-bindings":
        payload = envelope([], 0)
    elif path == "/admin/site-models":
        payload = envelope([], 0)
    else:
        payload = envelope([])
    route.fulfill(status=200, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))


def prepare_page(context, hash_path: str):
    page = context.new_page()
    page.route("**/admin/**", mock_admin)
    page.route("**/dashboard/**", mock_admin)
    page.goto(f"{BASE_URL}/web/console/#{hash_path}", wait_until="domcontentloaded", timeout=60000)
    page.wait_for_timeout(2000)
    return page


def render_cover(page):
    mark = Path("web/brand-mark.svg").read_text(encoding="utf-8")
    page.set_viewport_size({"width": 1600, "height": 900})
    page.set_content(
        f"""<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><style>
        *{{box-sizing:border-box}} body{{margin:0;background:#f3f7f5;color:#102c2b;font-family:Inter,'Segoe UI','Microsoft YaHei',sans-serif}}
        .cover{{position:relative;width:1600px;height:900px;overflow:hidden;padding:78px 86px;background:linear-gradient(135deg,#f8fbfa 0%,#edf6f2 100%)}}
        .top{{display:flex;align-items:center;gap:18px}} .mark{{width:58px;height:58px}} .mark svg{{width:100%;height:100%}}
        .brand{{font-size:28px;font-weight:800;letter-spacing:0}} .brand small{{display:block;margin-top:3px;color:#658079;font-size:14px;font-weight:600}}
        h1{{width:780px;margin:94px 0 22px;font-size:76px;line-height:1.04;letter-spacing:0}} h1 em{{color:#168264;font-style:normal}}
        .lead{{width:670px;margin:0;color:#506b64;font-size:24px;line-height:1.65}}
        .chips{{display:flex;gap:12px;margin-top:42px}} .chips span{{padding:12px 17px;color:#28564b;background:#fff;border:1px solid #d5e4de;border-radius:8px;font-size:15px;font-weight:700;box-shadow:0 8px 22px #183e3310}}
        .canvas{{position:absolute;right:-32px;bottom:-44px;width:760px;height:670px;transform:rotate(-2deg);background:#fff;border:1px solid #d7e5df;border-radius:18px;box-shadow:0 34px 80px #173f3430}}
        .canvas-head{{display:flex;height:68px;align-items:center;gap:10px;padding:0 25px;border-bottom:1px solid #e2ebe7}} .dot{{width:9px;height:9px;background:#53d6b1;border-radius:50%}} .canvas-head strong{{margin-left:5px;font-size:16px}}
        .flow{{position:relative;height:602px;padding:64px 54px}} .core{{position:absolute;top:222px;left:296px;display:grid;width:126px;height:126px;place-items:center;background:#102c2b;border-radius:28px;box-shadow:0 20px 40px #102c2b38}} .core svg{{width:72px;height:72px}}
        .node{{position:absolute;width:185px;padding:17px 18px;background:#f7faf9;border:1px solid #d9e6e1;border-radius:10px;box-shadow:0 12px 25px #173f3412}} .node b{{display:block;font-size:15px}} .node small{{display:block;margin-top:6px;color:#758b85;font-size:12px}} .n1{{top:74px;left:52px}} .n2{{top:75px;right:52px}} .n3{{bottom:96px;left:52px}} .n4{{right:52px;bottom:96px}}
        .line{{position:absolute;height:2px;transform-origin:left center;background:#9ccabe}} .l1{{top:189px;left:218px;width:155px;transform:rotate(25deg)}} .l2{{top:189px;left:389px;width:155px;transform:rotate(-25deg)}} .l3{{top:390px;left:218px;width:155px;transform:rotate(-25deg)}} .l4{{top:390px;left:389px;width:155px;transform:rotate(25deg)}}
        .pulse{{position:absolute;top:274px;left:349px;width:20px;height:20px;border:3px solid #53d6b1;border-radius:50%;animation:pulse 1.8s infinite}} @keyframes pulse{{to{{transform:scale(5);opacity:0}}}}
        .foot{{position:absolute;bottom:70px;left:86px;color:#70857f;font-size:15px}}
        </style></head><body><main class="cover"><div class="top"><div class="mark">{mark}</div><div class="brand">PivotFlow<small>枢衡</small></div></div><h1>把站点、模型与路由<br><em>收进一个控制面</em></h1><p class="lead">面向个人使用的 AI API 站点管理与智能路由平台。统一余额、签到、公告、模型测试和多协议分发。</p><div class="chips"><span>站点聚合</span><span>智能路由</span><span>多协议转换</span><span>本地优先</span></div><section class="canvas"><div class="canvas-head"><i class="dot"></i><strong>路由拓扑</strong></div><div class="flow"><i class="line l1"></i><i class="line l2"></i><i class="line l3"></i><i class="line l4"></i><div class="node n1"><b>Claude Code</b><small>Anthropic Messages</small></div><div class="node n2"><b>Codex</b><small>OpenAI Responses</small></div><div class="node n3"><b>聚合站点</b><small>余额 · 签到 · 公告</small></div><div class="node n4"><b>路由渠道</b><small>优先级 · 冷却 · 故障切换</small></div><div class="core">{mark}</div><i class="pulse"></i></div></section><div class="foot">github.com/yzgolden86/PivotFlow</div></main></body></html>""",
        wait_until="load",
    )
    page.screenshot(path=str(OUT / "pivotflow-cover.png"))


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(viewport=VIEWPORT, color_scheme="light")
        context.add_init_script(
            "localStorage.setItem('pivotflow_token','docs-sanitized-session');"
            "localStorage.setItem('pivotflow_token_expiry',String(Date.now()+3600000));"
            "localStorage.setItem('fusion_theme','light');"
        )

        dashboard = prepare_page(context, "/")
        dashboard.locator(".dashboard-page").wait_for()
        dashboard.screenshot(path=str(OUT / "dashboard.png"), full_page=True)
        dashboard.close()

        sites = prepare_page(context, "/sites")
        sites.locator(".site-row").first.wait_for()
        sites.screenshot(path=str(OUT / "sites-and-accounts.png"), full_page=True)
        sites.close()

        routing = prepare_page(context, "/channels")
        routing.locator(".channel-row").first.wait_for()
        routing.screenshot(path=str(OUT / "routing.png"), full_page=True)
        routing.close()

        cover = context.new_page()
        render_cover(cover)
        cover.close()
        browser.close()

    for image in sorted(OUT.glob("*.png")):
        print(f"generated {image}")


if __name__ == "__main__":
    main()
