#!/usr/bin/env python3
"""给控制台灌一批演示数据，用来评审 UI（图表、数字、列表都要有内容才看得出效果）。

用法：
    python scripts/seed_demo_data.py --db .tmp-smoke/smoke.db            # 灌数据
    python scripts/seed_demo_data.py --db .tmp-smoke/smoke.db --reset    # 先清空再灌

为什么要这个脚本，而不是靠界面手点：
  - 概览页的趋势图 / 消耗分配 / 工具消耗四张卡，**空库全都不渲染**，评审时看不出好坏；
  - 手动点出来的数据量太小、分布太假，趋势图会是一条直线；
  - 固定随机种子，同一份数据可以反复截图对比，视觉回归才有意义。

写入约束（踩过的坑，别改）：
  - `sites.proxy_url` / `sites.external_checkin_url` 在 schema 里可空，但 Go 扫描器
    按 string 扫 → 真写 NULL 会让 /admin/sites、/admin/dashboard 一起 500。
    **所有可空文本列一律写空串**，不要留 NULL。
  - `logs.time` 是**毫秒**，`minute_bucket = time / 60000`（迁移代码里就是这么回填的）。
  - `client_protocol` 只有 anthropic / codex / gemini / openai 会被概览页的
    「工具消耗」四张卡认出来，写别的值等于没写。
  - `site_channel_bindings` 是把 logs.channel_id 归到站点的唯一桥梁：没有 binding，
    「站点消耗分配」永远是空的，哪怕 logs 里 channel_id 填得再对。
"""

from __future__ import annotations

import argparse
import json
import random
import sqlite3
import time
from pathlib import Path

# 固定种子：同一份数据可反复复现，截图对比才有意义。
RANDOM_SEED = 20260917

DAY_MS = 86_400_000

# 模型 → (客户端协议, 每百万输入 token 价, 每百万输出 token 价)
# 价格只为让「消耗」看起来合理，不追求与厂商真实价目一致。
MODELS: dict[str, tuple[str, float, float]] = {
    "claude-sonnet-4-5": ("anthropic", 3.0, 15.0),
    "claude-opus-4-1": ("anthropic", 15.0, 75.0),
    "claude-haiku-4-5": ("anthropic", 1.0, 5.0),
    "gpt-5-codex": ("codex", 1.25, 10.0),
    "gpt-5": ("openai", 1.25, 10.0),
    "gpt-5-mini": ("openai", 0.25, 2.0),
    "gemini-2.5-pro": ("gemini", 1.25, 10.0),
    "gemini-2.5-flash": ("gemini", 0.3, 2.5),
}

# 模型权重：让消耗分配有明显的头部，不是均匀分布。
MODEL_WEIGHTS = {
    "claude-sonnet-4-5": 34,
    "claude-opus-4-1": 7,
    "claude-haiku-4-5": 12,
    "gpt-5-codex": 16,
    "gpt-5": 9,
    "gpt-5-mini": 8,
    "gemini-2.5-pro": 8,
    "gemini-2.5-flash": 6,
}

SITES = [
    # (name, platform, base_url, enabled, timezone, checkin_method, last_probe_status, tags)
    ("cun.ai", "new-api-family", "https://cun.ai", 1, "Asia/Shanghai", "turnstile", "ok", ["主力", "turnstile"]),
    ("agentrouter.org", "anyrouter", "https://agentrouter.org", 1, "Asia/Shanghai", "available", "ok", ["备用"]),
    ("veloera-demo", "veloera", "https://demo.veloera.example", 1, "Asia/Shanghai", "unknown", "unknown", ["自建"]),
    ("openai-relay", "openai-compatible", "https://relay.example.com", 0, "America/Los_Angeles", "disabled", "error", ["已停用"]),
]

# (site 序号, label, 状态, 余额, 币种, 是否启用)
ACCOUNTS = [
    (0, "cun 主号", "healthy", 42.75, "USD", 1),
    (0, "cun 备用号", "degraded", 3.10, "USD", 1),
    (1, "agentrouter 主号", "healthy", 18.40, "USD", 1),
    (1, "agentrouter 老号", "expired", 0.0, "USD", 1),
    (2, "veloera 自建", "unknown", 126.80, "CNY", 1),
    (3, "relay 已停用", "disabled", 0.0, "USD", 0),
]

# (name, url, channel_type, enabled, priority, rpm, concurrency)
CHANNELS = [
    ("cun-anthropic", "https://cun.ai/v1", "anthropic", 1, 100, 600, 8),
    ("cun-openai", "https://cun.ai/v1", "openai", 1, 90, 600, 8),
    ("agentrouter-gemini", "https://agentrouter.org/v1", "gemini", 1, 80, 300, 4),
    ("veloera-claude", "https://demo.veloera.example/v1", "anthropic", 1, 70, 300, 4),
    ("relay-openai", "https://relay.example.com/v1", "openai", 0, 10, 60, 2),
]

TOKENS = [
    ("pf-demo-claude-code", "本机 Claude Code", 1),
    ("pf-demo-codex-cli", "Codex CLI", 1),
    ("pf-demo-gemini-cli", "Gemini CLI", 1),
    ("pf-demo-old", "已停用的旧令牌", 0),
]

ANNOUNCEMENTS = [
    ("cun.ai 维护通知", "info", "## 维护窗口\n\n**9 月 20 日 02:00–04:00** 将进行上游网关升级，期间可能偶发 502。\n\n- 建议提前切换备用站点\n- 升级后模型列表会新增 `claude-sonnet-4-6`\n", 0),
    ("费率调整提醒", "warning", "## 费率调整\n\n`claude-opus-4-1` 输出单价上调，**10 月 1 日生效**。\n\n| 项目 | 原价 | 新价 |\n| --- | --- | --- |\n| 输入 | $15/M | $18/M |\n| 输出 | $75/M | $90/M |\n", 1),
    ("已恢复：上游 5xx 抖动", "info", "08:40 起上游出现约 12 分钟的 5xx 抖动，**已恢复**。\n\n期间失败请求不会计费，无需手动重试。\n", 1),
]


def reset(cur: sqlite3.Cursor) -> None:
    # 逐表删，别指望 ON DELETE CASCADE —— SQLite 实际 foreign_keys = 0，级联不触发。
    for table in ("site_channel_bindings", "site_accounts", "site_announcements",
                  "channel_models", "channel_url_states", "channel_model_cooldowns",
                  "channels", "auth_tokens", "logs", "sites"):
        cur.execute(f"DELETE FROM {table}")


def seed_sites(cur: sqlite3.Cursor, now_s: int) -> list[int]:
    ids: list[int] = []
    for name, platform, base_url, enabled, tz, method, probe, tags in SITES:
        cur.execute(
            """INSERT INTO sites
               (name, platform, base_url, enabled, timezone, use_system_proxy,
                proxy_url, external_checkin_url, tags_json, last_probe_status, last_error,
                checkin_method, checkin_method_checked_at, created_at, updated_at, deleted_at)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)""",
            (name, platform, base_url, enabled, tz, 1,
             "", "",  # 必须是空串，不能是 NULL
             json.dumps(tags, ensure_ascii=False), probe, "",
             method, now_s - 3600, now_s - 30 * 86400, now_s),
        )
        ids.append(cur.lastrowid)
    return ids


def seed_accounts(cur: sqlite3.Cursor, site_ids: list[int], now_s: int) -> list[int]:
    ids: list[int] = []
    for site_idx, label, status, balance, currency, enabled in ACCOUNTS:
        cur.execute(
            """INSERT INTO site_accounts
               (site_id, label, credential_type, credential_ciphertext, credential_key_version,
                enabled, suspended_by_site, auto_checkin, auto_refresh, timezone, status,
                balance, balance_currency, balance_updated_at, last_refresh_at, last_refresh_status,
                consecutive_failures, last_checkin_at, last_checkin_status, last_error,
                created_at, updated_at, deleted_at)
               VALUES (?,?,?,?,?,?,0,1,1,?,?,?,?,?,?,?,?,?,?,?,?,?,0)""",
            (site_ids[site_idx], label, "api_key", "demo-ciphertext", "v1",
             enabled, "Asia/Shanghai", status,
             balance, currency, now_s - 600, now_s - 600,
             "ok" if status == "healthy" else status,
             0 if status == "healthy" else 3,
             now_s - 6 * 3600, "success" if status == "healthy" else status, "",
             now_s - 20 * 86400, now_s),
        )
        ids.append(cur.lastrowid)
    return ids


def seed_channels(cur: sqlite3.Cursor, now_s: int) -> list[int]:
    ids: list[int] = []
    for name, url, ctype, enabled, priority, rpm, conc in CHANNELS:
        # channels.url 存的**不是 URL 字符串**，而是 ChannelURLs 的 JSON：
        #   [{"url": "...", "exact": false, "protocols": ["anthropic"]}]
        # 写成裸 URL 会让 model.ChannelURLs.Scan 报
        # `decode structured channel urls: invalid character 'h'`，
        # 而 /admin/dashboard、/admin/channels 会一起 500。
        urls_json = json.dumps([{"url": url}], ensure_ascii=False)
        cur.execute(
            """INSERT INTO channels
               (name, url, priority, rpm_limit, max_concurrency, channel_type, auth_type,
                oauth_credential, websockets, protocol_transform_mode, enabled, suspended_by_site,
                scheduled_check_enabled, scheduled_check_model, cooldown_until, cooldown_duration_ms,
                daily_cost_limit, cost_multiplier, custom_request_rules, cooldown_detection_rules,
                proxy_url, available_time_start, available_time_end, retry_other_keys_on_failure,
                created_at, updated_at)
               VALUES (?,?,?,?,?,?,?,?,0,'auto',?,0,0,'',0,0,0,1.0,'','','','','',0,?,?)""",
            (name, urls_json, priority, rpm, conc, ctype, "api_key", "", enabled,
             now_s - 20 * 86400, now_s),
        )
        ids.append(cur.lastrowid)
    return ids


def seed_bindings(cur: sqlite3.Cursor, account_ids: list[int], channel_ids: list[int], now_s: int) -> None:
    """把渠道挂到站点账号上。没有这一步，「站点消耗分配」永远是空的。"""
    pairs = [
        (account_ids[0], "cun-anthropic", channel_ids[0]),
        (account_ids[0], "cun-openai", channel_ids[1]),
        (account_ids[1], "cun-anthropic", channel_ids[0]),
        (account_ids[2], "agentrouter-gemini", channel_ids[2]),
        (account_ids[4], "veloera-claude", channel_ids[3]),
    ]
    for account_id, key, channel_id in pairs:
        cur.execute(
            """INSERT INTO site_channel_bindings
               (site_account_id, projection_key, channel_id, ownership, status, pricing_group,
                last_projected_hash, last_sync_status, last_sync_error, created_at, updated_at)
               VALUES (?,?,?,'owned','active','',?,'ok','',?,?)""",
            (account_id, key, channel_id, "0" * 64, now_s - 20 * 86400, now_s),
        )


def seed_tokens(cur: sqlite3.Cursor, now_s: int) -> list[int]:
    ids: list[int] = []
    for token, description, active in TOKENS:
        cur.execute(
            """INSERT INTO auth_tokens
               (token, token_ciphertext, token_hint, description, created_at, expires_at,
                last_used_at, is_active)
               VALUES (?,?,?,?,?,0,?,?)""",
            (token, "demo-ciphertext", f"{token[:8]}…", description,
             now_s - 30 * 86400, now_s - 120, active),
        )
        ids.append(cur.lastrowid)
    return ids


def seed_announcements(cur: sqlite3.Cursor, site_ids: list[int], now_s: int) -> None:
    for idx, (title, level, body, read) in enumerate(ANNOUNCEMENTS):
        site_id = site_ids[idx % len(site_ids)]
        cur.execute(
            """INSERT INTO site_announcements
               (site_id, source_key, title, content_markdown, level, source_url,
                upstream_created_at, upstream_updated_at, first_seen_at, last_seen_at,
                read_at, content_hash, created_at, updated_at)
               VALUES (?,?,?,?,?,'',?,?,?,?,?,?,?,?)""",
            (site_id, f"demo-{idx}", title, body, level,
             now_s - (idx + 1) * 3600, now_s - (idx + 1) * 3600,
             now_s - (idx + 1) * 3600, now_s - (idx + 1) * 3600,
             now_s - (idx + 1) * 3600 if read else 0,
             f"{idx:064d}", now_s - (idx + 1) * 3600, now_s),
        )


def seed_logs(cur: sqlite3.Cursor, channel_ids: list[int], token_ids: list[int], now_ms: int) -> int:
    rng = random.Random(RANDOM_SEED)
    model_keys = list(MODEL_WEIGHTS)
    weights = [MODEL_WEIGHTS[k] for k in model_keys]
    # 已停用的渠道（最后一个）不产生流量。
    live_channels = channel_ids[:-1]
    live_tokens = token_ids[:3]

    rows: list[tuple] = []
    for day_offset in range(30, -1, -1):
        day_start = now_ms - day_offset * DAY_MS
        if day_offset == 0:
            # 今天：从 00:00 到此刻，且给一个上午高、午后低的形状，趋势图才不是直线。
            midnight = day_start - (day_start % DAY_MS)
            span = max(now_ms - midnight, 60_000)
            count = int(span / 60_000 / 3)  # 平均每 3 分钟一条
        else:
            midnight = day_start - (day_start % DAY_MS)
            span = DAY_MS
            # 工作日多一点，周末少一点。
            weekday = time.gmtime(midnight / 1000).tm_wday
            count = rng.randint(70, 150) if weekday < 5 else rng.randint(25, 60)

        for _ in range(count):
            frac = rng.random()
            if day_offset == 0:
                # 工作日双峰：上午 10 点、下午 4 点。
                frac = min(0.98, abs(rng.gauss(0.55, 0.22)))
            ts = int(midnight + frac * span)
            if ts > now_ms:
                ts = now_ms - rng.randint(1, 600)

            model = rng.choices(model_keys, weights=weights, k=1)[0]
            protocol, in_rate, out_rate = MODELS[model]

            # 错误率 ~3.5%，其中大部分是 429。
            roll = rng.random()
            if roll < 0.025:
                status = 429
            elif roll < 0.032:
                status = 500
            elif roll < 0.035:
                status = 401
            else:
                status = 200

            streaming = 1 if rng.random() < 0.72 else 0
            if status == 200:
                input_tokens = int(rng.lognormvariate(7.6, 1.15))     # 中位数 ~2000
                output_tokens = int(rng.lognormvariate(6.4, 1.25))    # 中位数 ~600
                cache_read = int(input_tokens * rng.uniform(0, 0.7))
                cache_create = int(input_tokens * rng.uniform(0, 0.15))
            else:
                input_tokens = output_tokens = cache_read = cache_create = 0

            cost = (input_tokens * in_rate + output_tokens * out_rate
                    + cache_read * in_rate * 0.1 + cache_create * in_rate * 1.25) / 1_000_000
            duration = rng.uniform(0.6, 18.0) if streaming else rng.uniform(0.3, 6.0)

            rows.append((
                ts, ts // 60_000, model, "", "proxy",
                rng.choice(live_channels), status,
                "ok" if status == 200 else f"upstream status {status}",
                duration, streaming, 0,
                duration * rng.uniform(0.1, 0.4),
                "sk-demo", "0" * 64, rng.choice(live_tokens),
                protocol, "anthropic" if protocol == "anthropic" else "openai",
                f"10.0.{rng.randint(0, 5)}.{rng.randint(2, 250)}",
                "https://cun.ai/v1", "", "",
                input_tokens, output_tokens,
                int(output_tokens * rng.uniform(0, 0.35)), cache_read, cache_create,
                cache_create // 3, cache_create - cache_create // 3,
                round(cost, 8), 1.0, "site_pricing", "",
            ))

    cur.executemany(
        """INSERT INTO logs
           (time, minute_bucket, model, actual_model, log_source, channel_id, status_code, message,
            duration, is_streaming, upstream_websocket, first_byte_time,
            api_key_used, api_key_hash, auth_token_id,
            client_protocol, upstream_protocol, client_ip,
            base_url, service_tier, thinking_effort,
            input_tokens, output_tokens, reasoning_tokens, cache_read_input_tokens,
            cache_creation_input_tokens, cache_5m_input_tokens, cache_1h_input_tokens,
            cost, cost_multiplier, cost_source, rule_id)
           VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""",
        rows,
    )
    return len(rows)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--db", type=Path, required=True, help="SQLite 文件路径")
    parser.add_argument("--reset", action="store_true", help="先清空相关表再灌")
    args = parser.parse_args()

    if not args.db.is_file():
        print(f"error: {args.db} 不存在。先启动一次服务让它建库。")
        return 2

    now_s = int(time.time())
    now_ms = now_s * 1000

    db = sqlite3.connect(args.db)
    cur = db.cursor()
    try:
        if args.reset:
            reset(cur)
        cur.execute("SELECT COUNT(*) FROM sites")
        if cur.fetchone()[0] > 0:
            print("sites 表非空 —— 拒绝重复灌。要重来请加 --reset。")
            return 1

        site_ids = seed_sites(cur, now_s)
        account_ids = seed_accounts(cur, site_ids, now_s)
        channel_ids = seed_channels(cur, now_s)
        seed_bindings(cur, account_ids, channel_ids, now_s)
        token_ids = seed_tokens(cur, now_s)
        seed_announcements(cur, site_ids, now_s)
        log_count = seed_logs(cur, channel_ids, token_ids, now_ms)
        db.commit()
    finally:
        db.close()

    print(f"已灌入：{len(site_ids)} 站点 / {len(account_ids)} 账号 / "
          f"{len(channel_ids)} 渠道 / {len(token_ids)} 令牌 / {log_count} 条日志")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
