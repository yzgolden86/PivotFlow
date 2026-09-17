# PivotFlow 交接说明（Handoff）

面向接手本仓库、继续推进「站点自动签到」这条线的 Agent。读本文即可上手，不需要回看会话历史。

---

## 0. 当前状态

- 仓库：`E:\Dev\tools\api\PivotFlow`，分支 `main`
- 工作区**干净**。签到这条线的提交（由远及近）：

| Commit | 主题 |
| --- | --- |
| `63e2950` | 站点签到能力发现、失败分类、传输层与模型错误码修复（provider / storage / model 层） |
| `2122d29` | 签到结果落库、重试分档与历史保留期清扫（app 编排层） |
| `a6c82f9` | 控制台按签到能力收敛「打开签到页」入口 + 重建 `web/console` 产物 |
| `3e4d97d` | 补齐 Veloera 签到状态契约，收敛 Turnstile 复核承诺（§4 的 P1-0 ~ P1-3） |
| `7a24c24` | 记录 Veloera 不能委托 `ListModelsForRoutingKey` 的上游理由 |
| `36ee10d` | 识别阿里云 WAF 校验页，别再报成 invalid JSON |
| `380fbb4` | 站点请求统一带上 PivotFlow 的 User-Agent |

- 验证状态（**对 HEAD `380fbb4` 的独立复验，全绿**）：
  - `go build -tags sonic ./...` → 退出 0
  - `go test -tags sonic -count=1 ./internal/...` → 退出 0，33 个包全 `ok`
  - `golangci-lint run ./...`（v2.13.2）→ `0 issues.`
  - `gofmt -l internal/` → 无输出

  > 复验方式：每个提交都声称验证过，这里是**独立重跑**而非采信 commit message。
  > 另确认过工作区无残留半成品：`.tmp-sabotage/` 里破坏验证用的 `transport.bak`、
  > `newapi.final.bak` 与当前源码逐字一致，说明破坏已正确还原；最后一次提交与最后
  > 一次全绿日志同为 14:40，是先验证后提交。
  >
  > 复验时的坑：Git Bash 下 `export PATH="$PATH:$(go env GOPATH)/bin"` 无效
  > （`go env GOPATH` 是 Windows 格式，反斜杠路径 bash 认不了），`golangci-lint`
  > 会报 **exit 127（command not found）——这不是 lint 失败**。请用全路径
  > `/c/Users/80470/go/bin/golangci-lint.exe`，并放后台跑（约 4 分钟，前台会被 SIGTERM）。

**§4 的 P1-0 ~ P1-3 已完成**（`3e4d97d`）：`Veloera` 现按自己的
`/api/user/check_in_status` 实现 `CheckedInToday`；Turnstile 文案改为按能力位
决定是否承诺复核；已签到文案补「已经签到」；reward 增加 `data.quota` 回落。
**P1-4 结论不变**：AnyRouter 无状态端点，不补。§6 的三问 hao哥 已答复（Veloera /
AnyRouter 暂无可用站点；New API 已用真实令牌验证过；UTC 日界保留不处理）——
**这条线上已无待办项**，后续只在真接入 Veloera / AnyRouter 站点时回归验证即可。

三个平台的契约证据**已全部备齐**（Veloera 的 `/api/user/check_in`、
`/api/user/check_in_status`、`data.can_check_in`、成功响应的 `data.quota` 与
`model/user.go` 的「你今天已经签到过了」均已对着上游源码复核过），不需要再做
调研：

- **New API / Veloera** —— 直接读上游 Go 源码
- **AnyRouter** —— 闭源，反推自实战签到脚本 `millylee/anyrouter-check-in`（结论：没有状态端点，只能靠 POST 响应文案判断）

---

## 1. 本次会话已完成的改动

### 1.1 站点级签到能力发现（`63e2950`）

`GET /api/status` 是公开端点，无需凭证即可读 `checkin_enabled` / `turnstile_check` / `turnstile_site_key`。把解析抽成 `checkinMethodFromStatus(payload)`，`Detect()` 顺手填 `DetectionResult.CheckinMethod` —— 一次请求两用，零额外开销。

新增 `sites.checkin_method`（`unknown|available|disabled|turnstile`）+ `checkin_method_checked_at`，TTL 6h。这是**站点属性**不是账号属性，探测一次全站共享。

### 1.2 签到失败分类：让 `browser_required` 真正可达（`63e2950`）

**这是本次最核心的修复。** 上游 New API 系的所有签到失败都是 `HTTP 200 + success:false`（`controller/checkin.go` 的 `DoCheckin` 和 `middleware/turnstile-check.go` 都走 `c.JSON(http.StatusOK, ...)`），因此**传输层错误分支在真实站点上不可达**，判断失败原因不能只看错误码。

三个适配器统一改用 `checkinStatusFromCode(ErrorCode(responseError(payload, ...)))`，让载荷分支与传输层分支给出同一结论。在此之前，Turnstile 挑战被一律记成 `failed`：

- `last_checkin_status` 错标为 `failed`（应为 `browser_required`）
- `browser_required_count` 恒为 0
- 失败通知每天误报一次（而作者本意是排除它的 —— 见 `site_control.go` 里 `notifyCheckinFailure` 只在 `CheckinFailed` 时触发，以及控制台的 `['failed','browser_required']` 过滤）

### 1.3 reward 读取（`63e2950`）

新增 `checkinRewardText(data any)`：`data.reward` 优先（AnyRouter / 旧版预格式化串），回落 `data.quota_awarded` 渲染成 `+N 额度`。**不伪造货币** —— 额度到货币的汇率是站点设置，签到响应里不携带。

> 注意：`site_control.go` 在签到成功后若余额差为正，会用 `+%.2f CNY` **覆盖** `RewardText`。provider 这层只是兜底。

### 1.4 重试分两档（`2122d29`）

| 档位 | 间隔 × 上限 | 适用 |
| --- | --- | --- |
| 一般可恢复失败 | 1h × 16 | `failed` / `unsupported` / 未完成的 running |
| **人工挑战** | **12h × 2** | `browser_required` |

Turnstile 档**故意极慢**，理由是运营者（hao哥）明确提出的风控顾虑：服务端重试原理上不可能通过挑战，每次都是一发会被站点自身守卫拒掉的 POST；整天每小时重试正是风控会注意到的模式，而**封号代价远大于签到延迟**。人工可随时用控制台「重试」按钮即时触发，不受此限制。

> **口径提醒**：一次尝试 = 3 个请求（`GET /api/user/self` 刷余额 + `POST /api/user/checkin` + `GET /api/user/checkin?month=` 复核）。所以「每天 2 个请求」物理上不可能，讨论预算一律按**尝试次数**算。

回归测试 `TestChallengedDayIsCappedAtTwoAttempts` 用**行为**钉住预算：按 tick 精度走两天、模拟跨天拿到新行，**逐日**断言 2 次。两个反例别踩：断言常量会被静态检查当恒假；只断言两天总数会放过「只在第二天超标」（跨天会重置 `attempt_no`）。

### 1.5 其他（`2122d29` / `a6c82f9`）

- **定时任务先抢租约再落库**：反过来的写法会在公告刷新上每 tick 留一行废数据（公告租约 26h 且按天做 key，当天剩余每个 tick 都失败，约 900 行/站点/天）。手动任务保持先建行（客户端靠 `task_id` 轮询）。
- **保留期清扫**：30 天窗口，清终态 `site_tasks`、已结束 `checkin_runs`（连带 attempts）、已过期租约。
- **传输层**：`DialContext` 的 SSRF 私网过滤区分「拨代理」与「拨站点」（`proxyHopTracker`）。
- **控制台**：「打开签到页」入口改为按站点签到能力收敛（`turnstile` 显示、`disabled` 隐藏、未知时退回看最近签到结果）。

---

## 2. 环境与验证（必读）

```bash
# 构建与测试必须带 -tags sonic
go build -tags sonic ./...
go test  -tags sonic -count=1 ./internal/...

# lint 要求零警告（耗时 3-4 分钟，请后台跑，前台 120s 会被 SIGTERM）
golangci-lint run ./...

# 发布构建
CGO_ENABLED=0 go build -tags sonic -trimpath -ldflags=... -o pivotflow .
```

**本机只是开发环境，PivotFlow 实际跑在 VPS 的 Docker 里。** 本机 `data/pivotflow.db`、`.tmp-ui/pivotflow.db` 都是陈旧开发残留（旧 schema、0 行），**不能当线上库查签到历史**。要线上证据：向 hao哥 要 VPS 的 `docker logs` / 站点令牌，或直接读**上游开源源码**推导契约（本次会话就是靠这条定案的，最省事）。

其他坑见仓库记忆 `.workbuddy-ai/memory/MEMORY.md`（构建测试约定、数据模型硬约束、存储层约定、适配器要点、调度器要点、环境坑）。

---

## 3. 领域硬知识（别重新踩）

### 3.1 New API 系签到契约（读上游源码实测）

| 场景 | New API | Veloera | AnyRouter |
| --- | --- | --- | --- |
| 状态路由 | `GET /api/user/checkin?month=YYYY-MM`（**不挂** Turnstile） | `GET /api/user/check_in_status`（**注意下划线**） | **无**（闭源，实战脚本只用 self + sign_in） |
| 状态字段 | `data.stats.checked_in_today`: bool | `data.can_check_in`: bool（**语义相反**，true = 今天还没签） | — |
| 签到路由 | `POST /api/user/checkin`（**挂** Turnstile） | `POST /api/user/check_in`（**挂** Turnstile） | `POST /api/user/sign_in` |
| 成功响应 | `data.quota_awarded`: N | `data.quota`: N（**键名不同**） | 未知；实战脚本看 `ret==1 or code==0 or success` |
| 已签到文案 | `"今日已签到"` ✅ 已识别 | `"你今天已经签到过了"` ❌ **未识别** | 未知；实战脚本认 `已经签到`/`已签到`/`重复签到` |
| Turnstile 文案 | `"Turnstile token 为空"` ✅ | `"Turnstile token 为空"` ✅ | 未知 |

> 三者的证据强度不同：New API 与 Veloera 是直接读上游 Go 源码；AnyRouter 闭源，
> 结论反推自实战签到脚本 `millylee/anyrouter-check-in`，可信但非一手。

关键推论：

- `GET` 不挂 Turnstile 而 `POST` 挂，所以「人工在浏览器签完后由系统复核」这条恢复路径**原理上成立**。
- 对 Turnstile 站点，**服务端签到原理上不可能成功**。系统的职责是：①不误报失败 ②告诉运营者需人工 ③事后识别人工签到。①曾经是坏的，本次已修。

### 3.2 上游源码读法

```bash
# New API
https://raw.githubusercontent.com/QuantumNous/new-api/main/<path>
https://api.github.com/repos/QuantumNous/new-api/git/trees/main?recursive=1

# Veloera
https://raw.githubusercontent.com/Veloera/Veloera/main/<path>
```

关键文件：New API 的 `controller/checkin.go`、`model/checkin.go`、`middleware/turnstile-check.go`；Veloera 的 `router/api-router.go`（签到路由在第 80-81 行）、`controller/user.go`（`CheckInStatus` 第 1191 行、`CheckIn` 第 1212 行）、`model/user.go`（`CheckIn` 第 1128 行、`CanCheckInToday` 第 1235 行）。

### 3.3 数据模型硬约束

- **`checkin_attempts` UNIQUE `(site_account_id, local_day, trigger_scope)`**：每账号每天每触发范围只允许一行，「同一天再跑一次」必须 UPDATE 复用。定时签到 scope 是 `'daily'`，手动是 `'manual:<taskID>'`。`attempt_no` = 该行被写过几次。
- **SQLite 实际 `foreign_keys = 0`**，所有 `ON DELETE CASCADE` 都不触发。不能依赖级联删子表，写「删父行」逻辑要自己删子行。**不要顺手去开 `PRAGMA foreign_keys`** —— 会让全库删除行为突变，属独立高风险改动。
- `sql/site.go` 的 `siteColumns` 是站点查询唯一真源，`scanSite` 扫描顺序必须逐字对应，`CreateSite` 的 INSERT 列、`UpdateSite` 的 SET 列表同步。给 `model.Site` 加字段这**四处**要一起改。
- 后台任务写行要用窄更新（如 `UpdateSiteCheckinMethod` 只改两列），不要全行 `UpdateSite` —— 会覆盖控制台并发编辑。

### 3.4 适配器结构

`Veloera` / `AnyRouter` 用具名字段 `family *NewAPI` **包装而非嵌入**，**不继承方法**。新增 `NewAPI` 方法后要在这两个适配器上**显式委托**，否则类型断言失败（这正是 §4 缺口的成因）。

**但委托前必须对着上游路由确认一次**，「同一个 New API 家族」不等于「同一个接口」——两个已踩过的反例：

| 方法 | 结论 | 理由 |
| --- | --- | --- |
| `CheckedInToday` | Veloera **委托不了** | 家族版走 `/api/user/checkin`，Veloera 只有 `/api/user/check_in_status`，委托会 404（已按自有路由实现，`3e4d97d`） |
| `ListModelsForRoutingKey` | Veloera **不能委托**（保持不实现） | 路由 `/api/user/models` 存在，但 Veloera 的 `GetUserModels` **不读 `?group=`**，会返回用户全部可用模型；调用方把分组查询结果当该 Key 的权威清单，委托等于让 Key 路由到本组用不了的模型 |

判断依据是「上游该路由的入参与语义」，不是「路由存不存在」。实现处（`veloera.go`）都留了注释说明为什么不委托。

### 3.5 真实站点实测（2026-09-17）

对两个公开可达的真实站点实测过（本机经沙箱代理出口 `144.34.225.182`）：

| 站点 | 端点 | 结果 |
| --- | --- | --- |
| cun.ai | `GET /api/status` | HTTP 200 JSON：`checkin_enabled=true`、`turnstile_check=true`、`turnstile_site_key=0x4AAAAAADhQj66az9YHXxNJ`、`system_name=CUN.AI`、`version=2026.09.17-api-route-copy.1` |
| cun.ai | `GET /api/user/self`（无凭证） | HTTP 401 JSON：`{"message":"Unauthorized, not logged in and no access token provided","success":false}` |
| agentrouter.org | `GET /api/status` | HTTP 200 JSON（公开可读） |
| agentrouter.org | `GET /api/user/self`（无凭证） | HTTP 401 JSON：`{"message":"无权进行此操作，未登录且未提供 access token","success":false}` |

两点必须记下：

- **`/api/status` 不带 UA 也能正常拿到 200** —— 它是公开端点，CDN 不拦。要验证 UA 相关行为，必须用 `/api/user/self` 这类端点。
- **`380fbb4` 声称的「无 UA 会被 Cloudflare 403」复现不出来。** 用 Go 原生客户端（**不是 curl** —— curl 会发自己的头，不是忠实的替身）测了三种 UA：不设、显式 `Go-http-client/2.0`、显式 PivotFlow UA。cun.ai 与 agentrouter.org 的 `/api/user/self` **一律返回 401 标准 JSON，没有 403，也没有拦截页**。回显服务确认 Go 实际发出的是 `Go-http-client/2.0`，而非 commit message 里写的 `1.1`（那是旧版 Go 的默认值）。

  这不否定「统一 UA」本身：项目其他出站链路（版本检查、渠道健康检测）早已统一用 `version.OutboundUserAgent()`，站点链路用 Go 默认 UA 确实不一致，统一是合理且无害的。但**它的动机证据在本次网络路径下不成立**，接手者不要把它当成「必须保留，否则会被 CDN 拦」的硬约束。差异可能来自出口 IP 信誉或 Cloudflare 的动态策略。

---

## 4. 下一步（未完成的工作）

> **进度**：P1-0 ~ P1-3 **已完成**（`3e4d97d`），下面保留原始条目以便追溯，
> 每条标注了落点。P2 的 New API 部分已完成（真实站点令牌验证过）；
> Veloera / AnyRouter 因无可用站点无法验证。§6 的三个问题 hao哥 已全部答复。

### ~~P1-0~~（已完成，`3e4d97d`）让「由系统复核」这句话不再空头承诺

`internal/app/site_control.go` 约 1522 行：

```go
if methodKnown && method.Status == provider.CheckinMethodTurnstile && provider.ErrorCode(err) == provider.CodeBrowserRequired {
    result.Message = "站点启用了 Turnstile 人机验证，服务端无法自动完成；请在浏览器完成签到后由系统复核"
}
```

只有 `NewAPI` 实现了 `CheckinStatusProvider`，`Veloera` / `AnyRouter` 没有。这段话对后两者是**空头支票** —— 类型断言失败，复核根本不会跑。

两者的性质不同，别一概而论：

- **Veloera 是「暂时没实现」**：上游有 `/api/user/check_in_status`，可以补（见 P1-2）
- **AnyRouter 是「实现不了」**：上游根本没有状态端点，补不了（见 P1-4）

所以即便 P1-2 将来补齐了 Veloera，这句话对 AnyRouter 仍然必须收敛。这也是本项该先做的原因 —— 它是唯一能同时覆盖两者的修复。

**修法**：仅当 `adapter.(provider.CheckinStatusProvider)` 断言成功时才附加「由系统复核」；否则改成「请在浏览器完成签到」（不承诺复核）。

**验收**：加一个单测，用一个**不**实现 `CheckinStatusProvider` 的 stub 适配器 + turnstile 方法，断言消息里不含「由系统复核」；再用一个实现了的 stub，断言消息含之。

### ~~P1-1~~（已完成，`3e4d97d`）Veloera 已签到文案未被识别 → 每天 16 次无效重试

`isAlreadyCheckedMessage`（`anyrouter.go` 第 171 行）匹配 `already` / `已签到` / `重复签到` / `今日已`。

对 Veloera 的 `"你今天已经签到过了"`：

- `已签到`？→ 「已经签到过了」中「已」后面是「经」，**不连续**，不匹配
- `今日已`？→ 原文是「你今天已经」，不匹配
- 其余也不匹配 → **返回 false**

后果：落入 `!payload.Success` → `responseError` → 无 turnstile / 未登录关键词 → `CodeRequestFailed` → `checkinStatusFromCode` → **`CheckinFailed`**。

于是「今天已经签过」被记成失败：`last_checkin_status=failed`、触发失败通知，并且**当天按 1h × 16 重试**（最多 48 个请求）。这与 hao哥 明确提过的「别被当外部攻击、容易 ban 号」的顾虑**直接冲突**。

**不是拍脑袋**：实战签到脚本 `millylee/anyrouter-check-in` 的已签到关键词表是
`['已经签到', '已签到', '重复签到', 'already checked', 'already signed']` ——
**显式列了 `已经签到`**。说明 New API 系确实存在用「已经签到」措辞的分支，
而我们漏掉了它。（详见 P1-4）

**修法**：扩展 `isAlreadyCheckedMessage`，纳入 `已经签到`。注意别误伤其他分支（该函数被三个适配器共用）。

**验收**：单测，喂 `{"success":false,"message":"你今天已经签到过了"}` 给 `Veloera.Checkin`，断言 `Status == CheckinAlreadyChecked`；同时保留 New API 的 `"今日已签到"` 用例不回归。

### ~~P1-2~~（已完成，`3e4d97d`）Veloera 缺 `CheckedInToday`

**不要盲目委托 `family.CheckedInToday`** —— 它 GET `/api/user/checkin?month=`，而这个路由在 Veloera 上**不存在**（Veloera 是 `/api/user/check_in_status`），会 404。

**修法**：给 `Veloera` 实现自己的 `CheckedInToday`：

1. `GET /api/user/check_in_status`（走 `veloeraHeaders(req.Credentials)`）
2. 读 `data.can_check_in`
3. **`checkedInToday = !canCheckIn`**（语义相反，别搞反）

> 附带发现：Veloera 的 `CanCheckInToday()` 按 **UTC** 比较日期（`model/user.go` 第 1235 行），而 PivotFlow 用 `local_day`。跨时区站点可能错配 —— 先记下，不必现在动。

### ~~P1-3~~（已完成，`3e4d97d`）Veloera 的 reward 字段是 `quota`，不是 `quota_awarded`

Veloera 成功响应是 `{"success":true,"message":"签到成功","data":{"quota": reward}}`。`checkinRewardText` 目前只认 `reward` 和 `quota_awarded`，所以在 Veloera 上仍是空标签。

**修法**：在 `checkinRewardText` 里加 `quota` 回落。注意 `quota` 这个键在 `/api/user/self` 里是**余额**语义，但 `checkinRewardText` 只在签到响应上调用，所以安全 —— 改的时候确认调用点没变。

### P1-4 AnyRouter 没有状态端点——结论已定，别再找了

AnyRouter 本身闭源（`anyrouter/anyrouter` 仓库 404），但有多个实战签到脚本可反推契约。查 `millylee/anyrouter-check-in`（被 `rakuyoMo/autocheck-anyrouter` 等多个项目基于）确认：

- `utils/config.py`：`sign_in_path = '/api/user/sign_in'`、`user_info_path = '/api/user/self'`
- `checkin.py` 全流程**只有两个请求**：`GET /api/user/self`（拿余额，`quota/500000`）→ `POST /api/user/sign_in` → 再 `GET /api/user/self` 用**余额差**判断签到是否真的成功
- **没有任何状态查询端点**

结论：**AnyRouter 无法实现 `CheckedInToday`**，因此对 AnyRouter 的 Turnstile 站点，「由系统复核」必然是空头承诺。这正是 P1-0 存在的理由，别试图给它补一个。

顺带两个可借鉴的点（该脚本的成功判定比我们宽松）：

- 成功：`ret == 1 or code == 0 or success`（我们只看 `success`）
- 已签到关键词：`['已经签到', '已签到', '重复签到', 'already checked', 'already signed']`
  —— 注意它**显式列了 `已经签到`**，与下面 P1-1 的发现一致，可作为佐证。

### ~~P2~~（New API 部分已完成）真实站点验证

- **New API：已完成** —— hao哥 提供了若干 New API 站点令牌，zcode 据此做过真实站点验证。
- **Veloera / AnyRouter：无法完成** —— hao哥 答复目前没有这两个平台的可用站点，只能停留在上游源码推导层面（证据见 §3.1）。
- 补充：本次另对两个公开可达站点做了**无凭证**的端点实测（§3.5），可用于核对平台识别与 401 行为，但覆盖不到签到动作本身。

---

## 5. 关键文件索引

| 文件 | 作用 |
| --- | --- |
| `internal/site/provider/newapi.go` | New API 适配器；`checkinMethodFromStatus`、`checkinRewardText`、`CheckedInToday`、`responseError` |
| `internal/site/provider/anyrouter.go` | AnyRouter 适配器；`checkinStatusFromCode`、`isAlreadyCheckedMessage` |
| `internal/site/provider/veloera.go` | Veloera 适配器（**缺 `CheckedInToday`**） |
| `internal/site/provider/provider.go` | `CheckinStatusProvider` 等接口定义 |
| `internal/site/provider/transport.go` | 传输层 / 代理 / SSRF 过滤 |
| `internal/app/site_control.go` | 签到编排：`checkinWithTrigger`、Turnstile 消息、失败通知 |
| `internal/app/site_scheduler.go` | 调度器：`checkinRetryDue`、两档重试节奏 |
| `internal/app/site_retention.go` | 历史保留期清扫 |
| `internal/storage/sql/site.go` | 站点查询真源 + 保留期删除原语 |
| `internal/app/site_checkin_upstream_test.go` | **端到端接缝测试**：真实 `NewAPI` 适配器 + httptest 模拟上游 4 个端点 |
| `internal/site/provider/checkin_status_test.go` | 上游原文 fixture 的分类测试 |

---

## 6. 待决策问题（2026-09-17 已全部答复）

1. ~~Veloera / AnyRouter 有在用的站点吗？~~ → **答复：目前没有可用站点。**
   影响：P1-1 ~ P1-3 的修复**代码正确但短期内无人消费**，等将来真有 Veloera / AnyRouter 站点接入才会生效。P1-4 结论不变（AnyRouter 无状态端点，不补）。
2. ~~能否提供真实站点验证？~~ → **答复：已提供若干 New API 站点令牌，并由 zcode 完成过真实站点验证。**
   影响：P2 的 **New API 部分已覆盖**；Veloera / AnyRouter 因无可用站点，仍只能停留在源码推导层面。
3. ~~Veloera 的 UTC 日界与 `local_day` 可能错配，要处理吗？~~ → **答复：保留，不处理。** 不再跟进。

---

## 7. 工作纪律建议

- **每个修复都要用「临时破坏」证明它真的承重**：把改动回退 → 测试应该失败 → 恢复 → 转绿。
- **做破坏验证时用字节级替换**，别用 `python -c "..."` 里的 `\n\t\t` 转义（bash 双引号中不可靠，`str.replace` 不报错会导致「破坏没生效但测试通过」的假阴性）。用 `assert b.count(old)==1` + 改完 `grep` 确认破坏真的落地。
- **管道会吞退出码**：`go test ... | grep -vE "^ok "` 的 `$?` 是 grep 的（找不到行 = 1，看着像失败）；`... | tail -20` 的 `$?` 恒 0（掩盖失败）。判断成败请重定向到日志文件再直接看 `$?`。
- **`go vet` + `gofmt` 通过不代表 `staticcheck` 通过**（遇到过 `w.Write([]byte(fmt.Sprintf(...)))` 被 QF1012 拦）。
- 新增测试文件记得加 `//go:build sonic`。
