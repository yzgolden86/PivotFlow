# Balance and detail presentation follow-up

**Goal:** Address the six reported v0.6.3 balance, delayed pricing, detail-dialog, and request-history presentation issues.

**Design:** Retain the existing flat appearance, theme tokens, authentication boundaries and fixed-time daily balance contract. Distinguish absent comparison data from measured zero. Keep model card geometry stable as prices arrive. Use shared, compact grouped entity details and readable diagnostic cards. Allocate enough room for filter labels and retry sequences, with local scrolling for long histories.

**Scope:** Existing worktree `ui/stats-logs-layout-20261126`, baseline `d1b0533`; no automatic commit, push, release, real-data mutation or upstream probe.

## Tasks

- [x] Investigate scheduled balance collection and aggregation; reproduce any defect before a narrowly scoped backend fix. No invented historical samples or deltas.
- [x] Reserve pricing space and expose honest asynchronous loading in model cards without waiting for slow providers or fetching unbounded data.
- [x] Refine shared entity details and channel diagnostics, retaining all values, existing URLs and accessible close/focus behavior.
- [x] Fix the request-history date filter width and move transport/attempts left by rebalancing column tracks; keep channel and actual-model names accessible and long retry sequences scrollable.
- [x] Explain first baseline, missed sample or incomplete comparison with a concise empty state and on-demand details, never a fabricated zero line.
- [x] Run targeted regressions, whole frontend tests/type checks/build, relevant Go tests and viewport JPEG browser checks on desktop/mobile, including delayed API responses, long names and 8+ retries.

## Verification and review focus

Use test-first reproduction for behavior changes; record RED/GREEN below. Check 1440/1280/1024/390 viewports, dark appearance, keyboard focus/Escape, delayed pricing with no card movement, baseline versus true-zero balance data, and each existing entity-details caller. Preserve local scratch and earlier notes. Main owns builds and browser commands; workers use disjoint files.

## Progress

- Baseline: v0.6.3, clean tracked files; earlier SCRATCH and internal notes retained.
- Backend balance investigation: Beauvoir; model pricing: Ohm; entity details/diagnostics: Carver; history layout, balance states and integration: main.

## Completed behavior and limits

- SQLite lifecycle tests established the existing fixed-time contract: first successful day establishes a baseline; intraday balance refresh does not rewrite it; interrupted collection does not fabricate observations. No collector defect was established. The running user deployment and its actual daily observations have not been inspected.
- Added distinct baseline, empty, disabled, incomplete and noncomparable chart states. Only actual comparable zero deltas retain a zero reference. Explanations remain on demand; the overlay does not intercept chart touches.
- Model cards retain 288px height, a 49px pricing strip and bounded channel scrolling before and after price responses. Pricing has a three-request concurrency limit, represented-source priority, independent outcomes and explicitly marked stale prices after failed refreshes.
- Fixed representative-account pricing omissions and preserved tiny positive prices. Missing or malformed per-call prices are explicitly invalid rather than free; actual zero remains valid. Provider DTO extension is additive, without a schema change.
- Shared site/channel/token details now use grouped, aligned facts with two desktop columns and one mobile column; long model lists scroll locally. Diagnostics retain exact candidate identities and labeled metrics, blocking explanations and scoped navigation.
- Request-history date controls fit their labels at 1920/1440/1280/1024; transport/attempts start farther left and retain local scrolling through eight attempts.
- Final narrow-screen fix keeps status labels and icons intact and ellipsizes protocol/source labels within the fixed badge row. Full labels remain in DOM text and use the existing HelpTip interaction for hover, keyboard focus and touch/click disclosure, including Escape dismissal.

## Regression evidence

All paths below are local, synthetic test evidence under `SCRATCH/`; no production database, real administrator credentials or upstream probes were used.

- RED: `followup-touch-red` reproduced mobile chart taps intercepted by empty-state text; GREEN covered in the final follow-up suite.
- RED: `followup-account-scope-red` reproduced missing prices from a nonrepresentative account; GREEN covered by desktop/mobile account-scope cases.
- RED: `followup-layout-regression` reproduced differing card heights and dense-channel growth; GREEN `followup-final-layout/result.json`, 8/8 checks.
- RED: `followup-badges-red/result.json` reproduced 320px wide-font text wrapping outside the 25px badge row; GREEN `followup-completion-browser/result.json`, including 320/390px full-label and geometry assertions.
- Independent source review found that native titles alone did not provide dependable keyboard/touch disclosure. RED: `followup-badge-disclosure-red/result.json`; regression assertions now require touch and keyboard disclosure plus Escape dismissal for both badge labels.
- The follow-up review also required accessible button names to include their visible protocol/source labels; both names and assertions were corrected. Independent read-only source review then cleared this final delta.
- Final frontend: 514/514 `npm test`; `npm run typecheck` and `npm run build` exited 0. Logs: `followup-completion-frontend-tests.log`, `followup-completion-build.log`.
- Backend: `go test -tags sonic ./internal/...` passed, including new daily-balance lifecycle and pricing-validity tests. Log: `followup-go-tests.log`; no backend changes after this run.
- Final follow-up browser suite: `followup-completion-browser/result.json`, 32/32 checks, zero unexpected events, server closed.
- After the final accessible-name-only adjustment, rebuilt production assets and reran both 320/390px badge disclosure cases: `followup-final-badge-disclosure/result.json`, 2/2 checks, zero unexpected events, server closed. Touch, Tab focus, Escape, full names and stable geometry all passed.
- Request-history suite: `followup-final-history/result.json`, 22/22 checks, zero unexpected events, server closed. History source unchanged after this run.
- Daily/race audit: `followup-final-daily-audit/result.json`, 8/8 checks, zero unexpected events, server closed. Includes 121 pricing sites, concurrency/cancellation and stale response races; loader and daily-chart source unchanged after this run.
- Inspected viewport JPEGs for pending model prices, 320px wide-font badges, grouped channel details, channel diagnostics, mobile token details and history filter/attempt geometry. No full-page screenshots.
- `git diff --check` passed. Generated production assets were rebuilt. No commit, push, tag or release performed.
