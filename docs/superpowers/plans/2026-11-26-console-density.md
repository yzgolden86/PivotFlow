# Console density implementation plan

**Goal:** Implement the user's console layout corrections and resolve balance date and trace loading defects.
**Spec:** docs/superpowers/specs/2026-11-26-console-density-design.md
**Stack:** React 18, TypeScript, native CSS, Go/SQLite; existing Playwright synthetic harness.

## Constraints

Use current themes and existing HelpTip. Preserve known versus unavailable values. No invented daily deltas, no live-data edits, no release. Go commands use `-tags sonic`. Separate worktree based on 798f8ce.

## Review focus

Stale samples and entirely empty history still show the chosen calendar; range filters never truncate balance history; trace requests must stop after closure and timeout; isolated valid daily points stay discoverable; controls fit mobile and preserve keyboard access.

## Tasks

- [ ] 1. Balance dates: regression tests for sparse anchored 7/14 calendars and independent server history; add `balance_as_of_day` and render fixed calendars.
- [ ] 2. Request history: bounded read/retry/cache, canceled obsolete reads, remove note, slightly compact list. Verify automatic recovery and reopening through unit/browser checks.
- [ ] 3. Stats: independent longer trend range, adjacent charts, remove expanding fee evidence, move notes to HelpTip, scrollable complete distribution list.
- [ ] 4. Layout: two-line token identity/right details, compact model cards, paired health groups, inline backup hints, remove duplicated dashboard panels and unnecessary inline labels.
- [ ] 5. Verify types/tests/build, sonic tests; browser regression with sparse/dense synthetic data and responsive screenshots; start synthetic local preview for user.

## Progress

Initial source investigation: balance domain currently ends at latest record; incomplete history shortens the selected window; current API further clips history to current week. Request detail currently has neither a timeout nor automatic 404 recovery, and repeats a GET every opening. Storage detail is a single primary-key read with bounded JSON, so no evidence justifies storage schema changes.
