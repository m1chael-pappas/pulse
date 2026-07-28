# Changelog

All notable changes to pulse are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Installs track the default branch (`go install ...@latest`), so anything under
**Unreleased** is already live for anyone who installs or re-runs `go install`
today.

## [Unreleased]

### Fixed

- **Calendar tile dropped most events.** icalBuddy prints relative dates
  (`day after tomorrow at 10:30`) for the next few days, but the parser only
  understood `today`, `tomorrow`, and `YYYY-MM-DD`. Everything else failed to
  parse and was skipped silently — on a typical week that was every event from
  day three of the lookahead window onward. pulse now passes `-nrd` so
  icalBuddy emits absolute dates for everything.
- **All-day events vanished at midnight.** An all-day event parsed to
  `00:00–00:00`, so the "already finished" filter discarded it five minutes
  into the day it belonged to. Holidays and all-day markers now run to
  end-of-day and stay visible.
- **Parentheses stripped from event titles.** pulse passes `-nc`, so titles
  never carry a ` (calendar name)` suffix, but the code still removed the last
  parenthesised group — `Sprint showcase (+AI)` rendered as `Sprint showcase`.
  Titles are now used verbatim.
- **Claude costs were wrong for every Opus model.** Opus 4.6/4.7/4.8 were
  priced at $15/$75 per million tokens; the correct rate is $5/$25, so Opus
  usage was inflated roughly 3×.
- **Unhelpful error for a bad calendar name.** icalBuddy reports an unmatched
  `[maccal].calendars` entry as `No calendars.`, but the hint for that case
  matched lowercase `no calendar` only — so the tile showed the raw message
  instead of the fix. Matching is now case-insensitive, and the message lists
  the calendar names that do exist.

### Added

- **Pricing for current models.** Fable 5, Mythos 5, Opus 5, and Sonnet 5 were
  missing from the pricing table and silently fell back to Sonnet 4.6 rates —
  which badly under-counted Fable 5 in particular. All four are now priced
  explicitly, and the Claude tile labels them (`Fable 5`, `Opus 5`, `Sonnet 5`)
  instead of showing raw model IDs.
- **Tolerant model matching.** Context-window suffixes (`claude-opus-5[1m]`),
  dated variants (`claude-haiku-4-5-20251001`), and the bare `opus` / `sonnet`
  / `haiku` aliases found in older logs now resolve to the right model instead
  of falling through to the Sonnet fallback.
- **`pulse doctor` lists your calendar names.** `[maccal].calendars` matches a
  calendar's name, which is frequently not the account it syncs from — picking
  the account name yields an empty tile with no clue why. doctor now prints the
  exact strings to use.
- **Tests** for the calendar parser, the pricing table, and the error hints,
  covering each of the bugs above.

### Changed

- Placeholder `<synthetic>` log entries — internal Claude Code records that
  never hit the API and carry zero tokens — are no longer counted as calls or
  shown as a $0.00 row in the per-model breakdown.

### Notes

Cost figures will move after this release, in both directions: Opus usage gets
about 3× cheaper, while Fable 5 usage was previously billed at Sonnet rates and
gets substantially more expensive. Totals are still estimates — the Anthropic
Admin API remains the source of truth for what you were actually billed.
