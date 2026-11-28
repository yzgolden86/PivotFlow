# Intraday balance sampling implementation plan

**Goal:** Make today's balance net change visible during the day, retaining the existing 7/14-day daily chart.

**Design decision:** The user authorized increased sampling because a next-day-only result is too late. Use a configurable 60-minute default (implementation choice), with 15/30/60/120/360/720/1440-minute options. Existing `site_daily_balance_time` becomes the phase anchor, so default 00:05 produces hourly reads at HH:05. Existing enable preference is preserved.

**Approaches:** Merely polling the frontend cannot create upstream observations. Rewriting the existing immutable daily row would destroy its historical meaning. Instead, add immutable interval observations and aggregate them into the same daily presentation; preserve old daily records and their earlier graph points.

**Architecture:** New account/slot observations share existing control-plane task queue, refresh lease and provider budget. Reads never trigger check-in or model synchronization. Collect only the current slot and record actual completion time; restart does not fabricate missed slots. Default one request/account/hour, with a 35-second provider deadline bounded by slot end. Stored claims prevent duplicate reads in one slot. Retain 90 days with bounded background deletion.

**Daily calculation:** For matching account sets and currency, compare each account's latest observation on the day to its previous day's latest interval observation. When no previous-day interval observation exists, require at least two valid same-day observations per account and expose the measured first-to-latest difference as `comparison_status=partial`, `change_basis=first_sample`. A single observation remains a baseline. Latest failed/unknown reads, changed cohorts/currency and unsafe numbers never become a zero. New interval days override only their corresponding legacy days. The chart remains daily; partial-day meaning and latest observation time belong in existing on-demand explanations.

**Constraints:** Work in the existing linked worktree with earlier uncommitted changes preserved. No commit, push, release, production database access or live upstream probes. Go commands require `-tags sonic`. Synthetic browser evidence uses viewport JPEG only.

## Tasks

- [x] Storage: new sample table, claim/finish/list/90-day bounded deletion APIs and primary-backed Hybrid delegation; RED/GREEN SQLite lifecycle tests. Owner: Beauvoir.
- [x] Service: interval setting/default/validation, aligned-slot scheduling, existing task/lease budget integration and interval-reader lifecycle tests. Owner: main.
- [x] Aggregation: same-day partial progress, previous-day comparison, cohort/currency/failure safeguards and legacy-day compatibility; RED/GREEN tests. Owner: main.
- [x] Frontend: interval setting and schedule copy, valid partial values, first-sample basis tooltip and baseline waiting text; unit tests and type checks. Owner: Ohm.
- [x] Validation: full frontend suite/build, Go internal tests, synthetic desktop/mobile browser coverage, independent source review and final diff check. Runtime owner: main; read-only review/harness: Carver.

## Evidence and rulings

- Existing behavior confirmed: immutable one-row-per-account/day observations and fixed-time comparison; increasing render frequency alone would not satisfy the user.
- Existing old daily APIs/tests remain available for legacy history. New interval APIs reuse `model.SiteAccountDailyBalance` as an observation payload, with the new table's account/slot identity.
- New partial points explicitly represent observations since first same-day sample, not a fabricated whole calendar day's flow. This does not split credits and spending into separate ledgers.
- Independent review found and closed two aggregation edge cases with observed RED/GREEN tests: a later unsupported failure cannot restore an earlier successful reading for a participating account; an interval-history read error cannot restore a conflicting legacy result. Accounts that have only unsupported observations still do not block supported accounts.
- Actual `updated_at` appears in the on-demand chart readout, explicitly formatted in Beijing time even under a UTC browser. A single selected currency uses one readout column; no empty second column is reserved.
- `SCRATCH/interval-balance-internal-final.log`: full `go test -tags sonic ./internal/...` passed on the final backend sources. Storage migration regression covers default 60 and preservation of disabled/custom-time preferences; engine execution used temporary SQLite, not live MySQL/Postgres services.
- `SCRATCH/interval-balance-frontend-final.log`: all 532 frontend tests passed. No-emit app/test type checks passed; `SCRATCH/interval-balance-build-final-ui.log` records the final production asset build after the single-column readout and duplicate interval-unit display repairs.
- `SCRATCH/console-interval-balance-verified/result.json`: 14/14 desktop/mobile cases passed on the final build, including actual Beijing collection timestamps under a UTC browser, same-day positive-to-negative refresh, true zero versus missing values, 7/14-day switching, single-column readout width, and all settings interval choices/draft retention. No unexpected requests; owned server closed.
- `SCRATCH/interval-balance-legacy-layout-final/result.json`: 8/8 legacy daily graph and settings geometry cases passed. `SCRATCH/interval-balance-legacy-states/result.json`: 10/10 old baseline/zero/incomplete/empty/disabled cases passed. Legacy tests were updated only for the intentionally renamed switch and on-demand timestamp/basis disclosure.
- Visual QA inspected desktop/mobile viewport JPEGs. Independent source review closed all material findings. Final `git diff --check` passed, and all 48 referenced built assets resolve. No commit, push, release, production database or real upstream access was performed.
