"""Generate sanitized PivotFlow documentation images from the real console UI."""

import argparse
import json
import os
import traceback
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import Route, sync_playwright


BASE_URL = "http://127.0.0.1:8089"
OUT = Path("docs/assets")
VIEWPORT = {"width": 1600, "height": 1000}

NOW = 1_786_579_200_000
RETRY_SUCCESS_REQUEST_ID = "pf_req_0123456789abcdef0123456789abcdef"
COMMITTED_PARTIAL_REQUEST_ID = "pf_req_1123456789abcdef0123456789abcdef"
RATE_LIMITED_REQUEST_ID = "pf_req_2123456789abcdef0123456789abcdef"
CLIENT_CANCEL_REQUEST_ID = "pf_req_3123456789abcdef0123456789abcdef"
TOKEN_SCOPE_REQUEST_ID = "pf_req_" + format(0x4123456789abcdef0123456789abcdef, "032x")
PENDING_REQUEST_ID = "pf_req_5123456789abcdef0123456789abcdef"
MOCK_STATE = {
    "pending_404_count": 0,
    "last_list_query": None,
    "legacy_log_requests": 0,
    "legacy_bootstrap_requests": 0,
    "system_settings_requests": 0,
}

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

# Synthetic per-Key observations used by the Key health visual smoke test.
# This endpoint intentionally carries only masked identifiers, never credentials.
KEY_HEALTH = {
    "channel_id": 101, "channel_name": "星河主路由",
    "models": ["gpt-5.4", "claude-sonnet-4-6", "gemini-3-pro"],
    "keys": [
        {"id": 701, "key_index": 0, "masked_key": "sk-.A01", "note": "主账号 · 日常使用", "disabled": False, "cooldown_until": 0,
         "health": {"status": "healthy", "reason": "最近一次请求成功；不代表所有模型可用或余额充足", "status_code": 200, "checked_at": 1788766920000}},
        {"id": 702, "key_index": 1, "masked_key": "sk-.B02", "note": "备用账号 · 需要核对凭证", "disabled": False, "cooldown_until": 0,
         "health": {"status": "invalid", "reason": "认证失败，Key 可能无效、过期或已撤销，请核对后复检", "status_code": 401, "checked_at": 1788766860000}},
        {"id": 703, "key_index": 2, "masked_key": "sk-.C03", "note": "开发测试 · 月度额度", "disabled": False, "cooldown_until": 0,
         "health": {"status": "quota_exhausted", "reason": "上游提示余额或配额不足，请到服务商确认额度及恢复时间", "status_code": 429, "checked_at": 1788766800000}},
        {"id": 704, "key_index": 3, "masked_key": "sk-.D04", "note": "高峰期备用 · 稍后再试", "disabled": False, "cooldown_until": 0,
         "health": {"status": "rate_limited", "reason": "上游暂时限流，请稍后复检；不代表 Key 已失效", "status_code": 429, "checked_at": 1788766740000}},
        {"id": 705, "key_index": 4, "masked_key": "sk-.E05", "note": "新添加 · 尚未使用", "disabled": False, "cooldown_until": 0,
         "health": {"status": "", "reason": "", "status_code": 0, "checked_at": 0}},
        {"id": 706, "key_index": 5, "masked_key": "sk-.F06", "note": "归档账号 · 手动停用", "disabled": True, "cooldown_until": 0,
         "health": {"status": "", "reason": "", "status_code": 0, "checked_at": 0}},
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

AUTH_TOKENS = [
    {
        "id": 1,
        "token": "synthetic-token-one",
        "token_hint": "sk-•••A1",
        "description": "桌面工作流",
        "created_at": "2026-11-20T08:00:00+08:00",
        "is_active": True,
        "success_count": 128,
        "failure_count": 3,
        "prompt_tokens_total": 0,
        "completion_tokens_total": 0,
        "effective_cost_usd": 0,
        "cost_used_usd": 0,
        "cost_limit_usd": 0,
        "max_concurrency": 0,
    },
    {
        "id": 2,
        "token": "synthetic-token-two",
        "token_hint": "sk-•••B2",
        "description": "自动化任务",
        "created_at": "2026-11-20T08:00:00+08:00",
        "is_active": True,
        "success_count": 54,
        "failure_count": 1,
        "prompt_tokens_total": 0,
        "completion_tokens_total": 0,
        "effective_cost_usd": 0,
        "cost_used_usd": 0,
        "cost_limit_usd": 0,
        "max_concurrency": 0,
    },
]

REQUEST_TRACES = [
    {
        "request_id": RETRY_SUCCESS_REQUEST_ID,
        "started_at": NOW - 240_000,
        "auth_token_id": 1,
        "model": "claude-sonnet-4-6",
        "client_protocol": "anthropic",
        "transport": "http",
        "is_streaming": True,
        "final_status": 200,
        "outcome": "success",
        "duration": 4.82,
        "wait_duration": 0.24,
        "attempt_count": 3,
        "upstream_attempt_count": 2,
        "upstream_success_count": 1,
        "truncated": False,
    },
    {
        "request_id": COMMITTED_PARTIAL_REQUEST_ID,
        "started_at": NOW - 420_000,
        "auth_token_id": 1,
        "model": "claude-sonnet-4-6",
        "client_protocol": "anthropic",
        "transport": "http",
        "is_streaming": True,
        "final_status": 599,
        "outcome": "error",
        "duration": 3.14,
        "wait_duration": 0.11,
        "attempt_count": 1,
        "upstream_attempt_count": 1,
        "upstream_success_count": 0,
        "truncated": False,
    },
    {
        "request_id": RATE_LIMITED_REQUEST_ID,
        "started_at": NOW - 900_000,
        "auth_token_id": 1,
        "model": "gpt-5.4-codex",
        "client_protocol": "codex",
        "transport": "websocket",
        "is_streaming": True,
        "final_status": 429,
        "outcome": "error",
        "duration": 2.16,
        "wait_duration": 0.08,
        "attempt_count": 2,
        "upstream_attempt_count": 2,
        "upstream_success_count": 0,
        "truncated": False,
    },
    {
        "request_id": CLIENT_CANCEL_REQUEST_ID,
        "started_at": NOW - 1_500_000,
        "auth_token_id": 2,
        "model": "gemini-3-pro",
        "client_protocol": "gemini",
        "transport": "http",
        "is_streaming": True,
        "final_status": 499,
        "outcome": "canceled",
        "duration": 1.37,
        "wait_duration": 0.02,
        "attempt_count": 1,
        "upstream_attempt_count": 1,
        "upstream_success_count": 0,
        "truncated": False,
    },
    {
        "request_id": TOKEN_SCOPE_REQUEST_ID,
        "started_at": NOW - 2_100_000,
        "auth_token_id": 2,
        "client_protocol": "openai",
        "transport": "http",
        "is_streaming": False,
        "final_status": 401,
        "outcome": "error",
        "duration": 0.63,
        "wait_duration": 0,
        "attempt_count": 1,
        "upstream_attempt_count": 1,
        "upstream_success_count": 0,
        "truncated": True,
    },
]


def trace_attempt(number, started_at, *, outcome, reason, status_code, duration, retry="none", cooldown="none", upstream=True, response_committed=None, channel_id=101, channel_name="星河主路由", key_index=0, url_index=0, actual_model="claude-sonnet-4-6"):
    return {
        "number": number,
        "started_at": started_at,
        "duration": duration,
        "first_byte_time": round(duration * 0.42, 3),
        "channel_id": channel_id,
        "channel_name": channel_name,
        "key_index": key_index,
        "url_index": url_index,
        "endpoint": "https://api.example.net",
        "actual_model": actual_model,
        "upstream_protocol": "anthropic",
        "transport": "http",
        "status_code": status_code,
        "outcome": outcome,
        "upstream": upstream,
        "response_committed": outcome == "success" if response_committed is None else response_committed,
        "reason": reason,
        "retry": retry,
        "cooldown": cooldown,
        "cooldown_until": NOW + 60_000 if cooldown != "none" else 0,
    }


REQUEST_DETAILS = {
    RETRY_SUCCESS_REQUEST_ID: {
        **REQUEST_TRACES[0],
        "attempts": [
            trace_attempt(3, NOW - 237_000, outcome="success", reason="", status_code=200, duration=1.08),
            trace_attempt(1, NOW - 238_000, outcome="error", reason="upstream_auth", status_code=401, duration=0.34, retry="next_key", cooldown="key", key_index=1),
            trace_attempt(2, NOW - 239_200, outcome="skipped", reason="channel_concurrency_limit", status_code=429, duration=0.02, retry="next_channel", upstream=False, channel_name="星河主路由", key_index=2),
        ],
    },
    COMMITTED_PARTIAL_REQUEST_ID: {
        **REQUEST_TRACES[1],
        "attempts": [
            trace_attempt(1, NOW - 420_000, outcome="error", reason="stream_incomplete", status_code=599, duration=3.03, response_committed=True),
        ],
    },
    RATE_LIMITED_REQUEST_ID: {
        **REQUEST_TRACES[2],
        "attempts": [
            trace_attempt(1, NOW - 900_000, outcome="error", reason="rate_limited", status_code=429, duration=1.02, retry="next_channel", cooldown="model", actual_model="gpt-5.4-codex"),
            trace_attempt(2, NOW - 898_000, outcome="error", reason="upstream_error", status_code=503, duration=1.14, channel_id=102, channel_name="远岚 Codex", actual_model="gpt-5.4-codex"),
        ],
    },
    CLIENT_CANCEL_REQUEST_ID: {
        **REQUEST_TRACES[3],
        "attempts": [
            trace_attempt(1, NOW - 1_500_000, outcome="canceled", reason="client_canceled", status_code=499, duration=1.37, actual_model="gemini-3-pro"),
        ],
    },
    TOKEN_SCOPE_REQUEST_ID: {
        **REQUEST_TRACES[4],
        "attempts": [
            {
                "number": 1,
                "started_at": NOW - 2_100_000,
                "duration": 0.63,
                "first_byte_time": 0.2,
                "upstream_protocol": "openai",
                "transport": "http",
                "status_code": 401,
                "outcome": "error",
                "upstream": True,
                "response_committed": False,
                "reason": "upstream_auth",
                "retry": "none",
                "cooldown": "none",
                "cooldown_until": 0,
            }
        ],
    },
    PENDING_REQUEST_ID: {
        **REQUEST_TRACES[0],
        "request_id": PENDING_REQUEST_ID,
        "attempts": [
            trace_attempt(1, NOW - 120_000, outcome="success", reason="", status_code=200, duration=0.9),
        ],
    },
}


def request_trace_stats(items):
    return {
        "requests": len(items),
        "successes": sum(item["outcome"] == "success" for item in items),
        "errors": sum(item["outcome"] == "error" for item in items),
        "canceled": sum(item["outcome"] == "canceled" for item in items),
        "upstream_attempts": sum(item["upstream_attempt_count"] for item in items),
        "upstream_successes": sum(item["upstream_success_count"] for item in items),
    }


def envelope(data, count=None):
    payload = {"success": True, "data": data}
    if count is not None:
        payload["count"] = count
    return payload


def mock_admin(route: Route):
    parsed = urlparse(route.request.url)
    path = parsed.path
    query = parse_qs(parsed.query)
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
    elif path == "/admin/logs":
        MOCK_STATE["legacy_log_requests"] += 1
        payload = envelope([], 0)
    elif path == "/admin/logs/bootstrap":
        MOCK_STATE["legacy_bootstrap_requests"] += 1
        payload = envelope({"channel_test_content": "", "auth_tokens": [], "models": [], "channels": [], "status_codes": []})
    elif path == "/admin/settings":
        MOCK_STATE["system_settings_requests"] += 1
        payload = envelope([])
    elif path == "/admin/auth-tokens":
        payload = envelope({"tokens": AUTH_TOKENS, "is_today": True})
    elif path == "/admin/request-traces":
        MOCK_STATE["last_list_query"] = {key: values[-1] for key, values in query.items()}
        items = REQUEST_TRACES
        if query.get("outcome"):
            items = [item for item in items if item["outcome"] == query["outcome"][0]]
        if query.get("auth_token_id"):
            items = [item for item in items if item["auth_token_id"] == int(query["auth_token_id"][0])]
        if query.get("search"):
            needle = query["search"][0].lower()
            items = [
                item for item in items
                if needle in item["request_id"].lower() or needle in item.get("model", "").lower()
            ]
        limit = int(query.get("limit", ["20"])[0])
        offset = int(query.get("offset", ["0"])[0])
        payload = envelope(items[offset:offset + limit], len(items))
    elif path == "/admin/request-traces/stats":
        items = REQUEST_TRACES
        if query.get("outcome"):
            items = [item for item in items if item["outcome"] == query["outcome"][0]]
        if query.get("auth_token_id"):
            items = [item for item in items if item["auth_token_id"] == int(query["auth_token_id"][0])]
        if query.get("search"):
            needle = query["search"][0].lower()
            items = [
                item for item in items
                if needle in item["request_id"].lower() or needle in item.get("model", "").lower()
            ]
        payload = envelope(request_trace_stats(items))
    elif path.startswith("/admin/request-traces/"):
        request_id = path.rsplit("/", 1)[-1]
        if request_id == PENDING_REQUEST_ID and MOCK_STATE["pending_404_count"] == 0:
            MOCK_STATE["pending_404_count"] += 1
            route.fulfill(
                status=404,
                content_type="application/json",
                body=json.dumps({"success": False, "error": "not_found", "data": {"message": "request trace not found"}}),
            )
            return
        detail = REQUEST_DETAILS.get(request_id)
        if detail is None:
            route.fulfill(
                status=404,
                content_type="application/json",
                body=json.dumps({"success": False, "error": "not_found", "data": {"message": "request trace not found"}}),
            )
            return
        payload = envelope(detail)
    elif path == "/admin/site-channel-bindings":
        payload = envelope([], 0)
    elif path == "/admin/site-models":
        payload = envelope([], 0)
    else:
        payload = envelope([])
    route.fulfill(status=200, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))


def prepare_page(context, hash_path: str, base_url: str = BASE_URL):
    page = context.new_page()
    page.route("**/admin/**", mock_admin)
    page.route("**/dashboard/**", mock_admin)
    page.goto(f"{base_url}/web/console/#{hash_path}", wait_until="domcontentloaded", timeout=60000)
    page.wait_for_timeout(2000)
    return page


def render_cover(page, output: Path = OUT):
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
    page.screenshot(path=str(output / "pivotflow-cover.png"))


def qa_check(checks, name, condition, detail):
    checks.append({"name": name, "status": "pass" if condition else "fail", "detail": detail})
    if not condition:
        raise AssertionError(f"{name}: {detail}")


def qa_context(browser, width, height, theme, base_url):
    context = browser.new_context(viewport={"width": width, "height": height}, color_scheme=theme)
    context.add_init_script(
        "localStorage.setItem('pivotflow_token','request-history-qa');"
        "localStorage.setItem('pivotflow_token_expiry',String(Date.now()+3600000));"
        f"localStorage.setItem('fusion_theme','{theme}');"
    )
    context.grant_permissions(["clipboard-read", "clipboard-write"], origin=base_url)
    return context


def qa_open_page(context, base_url, hash_path, checks, events, allow_404=False):
    page = context.new_page()
    page.on("console", lambda message: events.append({
        "kind": "console",
        "type": message.type,
        "text": message.text,
        "expected": allow_404 and message.type == "error" and "404" in message.text,
    }) if message.type == "error" else None)
    page.on("pageerror", lambda error: events.append({"kind": "pageerror", "text": str(error), "expected": False}))
    page.on("requestfailed", lambda request: events.append({"kind": "requestfailed", "text": request.url, "expected": False}))
    page.route("**/admin/**", mock_admin)
    page.route("**/dashboard/**", mock_admin)
    page.goto(f"{base_url}/web/console/#{hash_path}", wait_until="networkidle", timeout=60000)
    # 控制台会在空闲时预取各页面 chunk；等预取落地后再操作，避免关页时把
    # 仍在途的请求记成失败。
    page.wait_for_timeout(1200)
    qa_check(checks, "page loaded", page.locator(".workspace-page").count() == 1, hash_path)
    return page


def qa_wait_rows(page, count):
    page.wait_for_function("count => document.querySelectorAll('.request-ledger-row').length === count", arg=count)


def qa_capture(page, output, name, checks):
    path = output / f"{name}.jpg"
    page.screenshot(path=str(path), type="jpeg", quality=88, animations="disabled")
    size = page.viewport_size
    qa_check(checks, f"screenshot {name}", path.read_bytes()[:2] == b"\xff\xd8", str(path))
    qa_check(checks, f"viewport {name}", bool(size), f"{size['width']}x{size['height']}")
    return str(path)


def qa_capture_scrolled(page, output, name, checks, scroll_top):
    page.evaluate("top => { const modal = document.querySelector('.console-modal'); if (modal) modal.scrollTop = top }", scroll_top)
    page.wait_for_timeout(150)
    return qa_capture(page, output, name, checks)


def run_request_history_qa(output: Path, base_url: str):
    output.mkdir(parents=True, exist_ok=True)
    base_url = base_url.rstrip("/")
    MOCK_STATE["pending_404_count"] = 0
    MOCK_STATE["last_list_query"] = None
    MOCK_STATE["legacy_log_requests"] = 0
    MOCK_STATE["legacy_bootstrap_requests"] = 0
    MOCK_STATE["system_settings_requests"] = 0
    checks = []
    screenshots = []
    browser_events = []
    failure = None
    browser = None

    def write_result_snapshot(current_failure):
        unexpected = [event for event in browser_events if not event.get("expected")]
        result = {
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "mode": "request-history-qa",
            "base_url": base_url,
            "passed": current_failure is None and not unexpected,
            "failure": current_failure,
            "checks": checks,
            "screenshots": screenshots,
            "browser_events": browser_events,
            "unexpected_browser_events": unexpected,
            "mock_state": MOCK_STATE,
        }
        result_path = output / "qa-result.json"
        result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"generated {result_path}")
        return unexpected

    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(
                headless=True,
                executable_path=os.environ.get("PIVOTFLOW_QA_CHROMIUM") or None,
                args=["--no-proxy-server"],
            )

            light = qa_context(browser, 1440, 900, "light", base_url)
            page = qa_open_page(light, base_url, "/logs?view=requests", checks, browser_events)
            qa_wait_rows(page, len(REQUEST_TRACES))
            qa_check(
                checks,
                "request view skips legacy log bootstrap",
                MOCK_STATE["legacy_log_requests"] == 0 and MOCK_STATE["legacy_bootstrap_requests"] == 0 and MOCK_STATE["system_settings_requests"] == 0,
                str(MOCK_STATE),
            )
            qa_check(
                checks,
                "search placeholder matches API fields",
                page.get_by_label("搜索完整请求").get_attribute("placeholder") == "搜索请求 ID / 模型",
                page.get_by_label("搜索完整请求").get_attribute("placeholder") or "",
            )
            qa_check(checks, "desktop light theme", page.evaluate("document.documentElement.dataset.theme") == "light", "data-theme=light")
            qa_check(checks, "complete requests is default tab", page.get_by_role("tab", name="完整请求").get_attribute("aria-selected") == "true", "aria-selected=true")
            screenshots.append(qa_capture(page, output, "desktop-1440-light-list", checks))

            page.get_by_label("最终结果").select_option("error")
            qa_wait_rows(page, 3)
            rows = page.locator(".request-ledger-row").all_inner_texts()
            qa_check(checks, "outcome filter", len(rows) == 3 and all("失败" in row for row in rows), "three final errors")

            page.get_by_label("最终结果").select_option("")
            qa_wait_rows(page, len(REQUEST_TRACES))
            page.get_by_label("完整请求访问令牌").select_option("2")
            qa_wait_rows(page, 2)
            qa_check(checks, "token filter request", MOCK_STATE["last_list_query"].get("auth_token_id") == "2", str(MOCK_STATE["last_list_query"]))
            page.get_by_label("清除完整请求筛选").click()
            qa_wait_rows(page, len(REQUEST_TRACES))
            retry_search = RETRY_SUCCESS_REQUEST_ID
            page.get_by_label("搜索完整请求").fill(retry_search)
            qa_wait_rows(page, 1)
            qa_check(checks, "search filter", RETRY_SUCCESS_REQUEST_ID in page.locator(".request-ledger-row").inner_text(), MOCK_STATE["last_list_query"]["search"])
            page.get_by_label("清除完整请求筛选").click()
            qa_wait_rows(page, len(REQUEST_TRACES))
            page.get_by_label("完整请求时间范围").select_option("this_week")
            qa_wait_rows(page, len(REQUEST_TRACES))
            qa_check(checks, "range filter request", MOCK_STATE["last_list_query"].get("range") == "this_week", str(MOCK_STATE["last_list_query"]))
            page.get_by_label("完整请求时间范围").select_option("today")
            qa_wait_rows(page, len(REQUEST_TRACES))
            with page.expect_response(lambda response: "/admin/request-traces?" in response.url):
                page.get_by_label("刷新完整请求").click()
            qa_wait_rows(page, len(REQUEST_TRACES))
            qa_check(
                checks,
                "request refresh skips legacy log bootstrap",
                MOCK_STATE["legacy_log_requests"] == 0 and MOCK_STATE["legacy_bootstrap_requests"] == 0 and MOCK_STATE["system_settings_requests"] == 0,
                str(MOCK_STATE),
            )

            row = page.get_by_role("button", name=f"查看请求 {RETRY_SUCCESS_REQUEST_ID} 详情")
            row.focus()
            row.press("Enter")
            dialog = page.get_by_role("dialog")
            dialog.wait_for()
            qa_check(checks, "dialog receives focus", page.evaluate("!!document.activeElement?.closest('.console-modal')"), "focus inside dialog")
            page.keyboard.press("Escape")
            dialog.wait_for(state="hidden")
            qa_check(checks, "focus returns to trigger", page.evaluate("document.activeElement?.getAttribute('aria-label')") == f"查看请求 {RETRY_SUCCESS_REQUEST_ID} 详情", "trigger focused")

            partial_row = page.get_by_role("button", name=f"查看请求 {COMMITTED_PARTIAL_REQUEST_ID} 详情")
            partial_row_text = partial_row.inner_text()
            qa_check(checks, "row internal 599 status label", "状态 流不完整" in partial_row_text and "HTTP 599" not in partial_row_text, partial_row_text.replace("\n", " | "))
            partial_row.click()
            dialog = page.get_by_role("dialog")
            dialog.wait_for()
            partial_text = dialog.inner_text()
            qa_check(checks, "detail internal 599 status label", "状态 流不完整" in partial_text and "HTTP 599" not in partial_text, "internal status not presented as wire HTTP")
            attempt_text = page.locator(".request-timeline-event").first.inner_text()
            qa_check(checks, "attempt internal 599 status label", "状态 流不完整" in attempt_text and "HTTP 599" not in attempt_text, attempt_text.replace("\n", " | "))
            qa_check(checks, "committed stream error explanation", "响应已开始，未重放" in attempt_text, "committed error not replayed")
            screenshots.append(qa_capture(page, output, "desktop-1440-light-partial599-detail", checks))
            page.keyboard.press("Escape")
            dialog.wait_for(state="hidden")
            light.close()

            dark = qa_context(browser, 1440, 900, "dark", base_url)
            page = qa_open_page(dark, base_url, f"/logs?view=requests&request={RETRY_SUCCESS_REQUEST_ID}", checks, browser_events)
            dialog = page.get_by_role("dialog")
            dialog.wait_for()
            qa_check(checks, "desktop dark theme", page.evaluate("document.documentElement.dataset.theme") == "dark", "data-theme=dark")
            qa_check(checks, "request deep link", RETRY_SUCCESS_REQUEST_ID in dialog.inner_text(), page.url)
            markers = page.locator(".request-timeline-marker").all_inner_texts()
            qa_check(checks, "timeline order by attempt number", markers == ["1", "2", "3"], str(markers))
            body_text = "\n".join(page.locator(".request-timeline-signals").all_inner_texts())
            qa_check(checks, "human reason labels", "上游认证失败" in body_text and "渠道并发已满" in body_text, body_text.replace("\n", " | "))
            qa_check(checks, "raw reason codes hidden", "upstream_auth" not in body_text and "channel_concurrency_limit" not in body_text, "no visible fixed codes")
            screenshots.append(qa_capture(page, output, "desktop-1440-dark-detail", checks))

            page.get_by_role("button", name="复制诊断 JSON").click()
            page.get_by_role("button", name="已复制").wait_for()
            diagnostic = json.loads(page.evaluate("navigator.clipboard.readText()"))
            qa_check(checks, "copy diagnostic JSON", diagnostic["request_id"] == RETRY_SUCCESS_REQUEST_ID and len(diagnostic["attempts"]) == 3, "request_id and 3 attempts")
            qa_check(checks, "diagnostic has no injected secret", "authorization" not in json.dumps(diagnostic), "sanitized payload")
            dark.close()

            mobile = qa_context(browser, 390, 844, "light", base_url)
            page = qa_open_page(mobile, base_url, f"/logs?view=requests&request={TOKEN_SCOPE_REQUEST_ID}", checks, browser_events)
            dialog = page.get_by_role("dialog")
            dialog.wait_for()
            qa_check(checks, "token-scoped placeholders", all(text in dialog.inner_text() for text in ["渠道信息不可见", "凭证与 URL 序号不可见", "上游模型不可见"]), "missing internal fields handled")
            qa_check(checks, "390 viewport rendered", page.evaluate("window.innerWidth") == 390, "390x844")
            screenshots.append(qa_capture(page, output, "mobile-390-light-token-scope", checks))
            screenshots.append(qa_capture_scrolled(page, output, "mobile-390-light-token-scope-timeline", checks, 540))

            page.close()
            page = qa_open_page(mobile, base_url, f"/logs?view=requests&request={PENDING_REQUEST_ID}", checks, browser_events, allow_404=True)
            page.get_by_text("请求仍在执行或记录尚未保存，请稍后重试").wait_for()
            qa_check(checks, "404 pending explanation", page.get_by_text("请求记录尚未就绪").is_visible(), "pending, not permanent failure")
            screenshots.append(qa_capture(page, output, "mobile-390-light-pending-404", checks))
            page.get_by_role("button", name="重新检查").click()
            page.get_by_role("dialog").get_by_text(PENDING_REQUEST_ID).first.wait_for()
            qa_check(checks, "404 retry succeeds", MOCK_STATE["pending_404_count"] == 1, "one 404 then persisted response")
            mobile.close()

            narrow = qa_context(browser, 320, 760, "dark", base_url)
            page = qa_open_page(narrow, base_url, f"/logs?view=requests&request={RETRY_SUCCESS_REQUEST_ID}", checks, browser_events)
            dialog = page.get_by_role("dialog")
            dialog.wait_for()
            qa_check(checks, "320 dark viewport rendered", page.evaluate("window.innerWidth") == 320 and page.evaluate("document.documentElement.dataset.theme") == "dark", "320x760 dark")
            qa_check(checks, "320 no page horizontal overflow", page.evaluate("document.documentElement.scrollWidth <= window.innerWidth + 1"), "document width within viewport")
            screenshots.append(qa_capture(page, output, "mobile-320-dark-detail", checks))
            screenshots.append(qa_capture_scrolled(page, output, "mobile-320-dark-detail-timeline", checks, 520))
            narrow.close()

            # 先落盘本轮结果，再执行浏览器关闭。部分 Windows 环境会在 close
            # 阶段等待较久，不能让已完成的截图和断言因此丢失。
            unexpected_snapshot = write_result_snapshot(None)
            if unexpected_snapshot:
                raise RuntimeError(f"unexpected browser events: {unexpected_snapshot}")
            browser.close()
            browser = None
    except Exception:
        failure = traceback.format_exc()
    finally:
        if browser is not None:
            try:
                browser.close()
            except Exception:
                if failure is None:
                    failure = traceback.format_exc()

    unexpected_events = write_result_snapshot(failure)
    if failure is not None:
        raise RuntimeError(failure)
    if unexpected_events:
        raise RuntimeError(f"unexpected browser events: {unexpected_events}")


def main(output: Path = OUT, base_url: str = BASE_URL):
    output.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(viewport=VIEWPORT, color_scheme="light")
        context.add_init_script(
            "localStorage.setItem('pivotflow_token','docs-sanitized-session');"
            "localStorage.setItem('pivotflow_token_expiry',String(Date.now()+3600000));"
            "localStorage.setItem('fusion_theme','light');"
        )

        dashboard = prepare_page(context, "/", base_url)
        dashboard.locator(".dashboard-page").wait_for()
        dashboard.screenshot(path=str(output / "dashboard.png"), full_page=True)
        dashboard.close()

        sites = prepare_page(context, "/sites", base_url)
        sites.locator(".site-row").first.wait_for()
        sites.screenshot(path=str(output / "sites-and-accounts.png"), full_page=True)
        sites.close()

        routing = prepare_page(context, "/channels", base_url)
        routing.locator(".channel-row").first.wait_for()
        routing.screenshot(path=str(output / "routing.png"), full_page=True)
        routing.close()

        cover = context.new_page()
        render_cover(cover, output)
        cover.close()
        browser.close()

    for image in sorted(output.glob("*.png")):
        print(f"generated {image}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--request-history-qa", action="store_true")
    parser.add_argument("--base-url", default=BASE_URL)
    parser.add_argument("--output", type=Path, default=OUT)
    args = parser.parse_args()
    if args.request_history_qa:
        run_request_history_qa(args.output, args.base_url)
    else:
        main(args.output, args.base_url)
