# Copilot usage sources

The AI usage tile reads Copilot data from two places.

## Account spend

`gh api /copilot_internal/user` returns `quota_snapshots`, each with a `credits_used` count for the current billing month.
The tile sums them and shows the total as dollars.
This covers every Copilot client on the account, not only the CLI.

The endpoint is internal and GitHub does not document it.
If it changes, the spend window disappears and the per-model rows stay.
The tile calls it at most once every five minutes.

One GitHub AI credit costs $0.01.
Source: https://docs.github.com/copilot/concepts/billing/usage-based-billing-for-individuals

## Per-model usage

The Copilot CLI records each request in `~/.copilot/session-store.db`, table `assistant_usage_events`.
`total_nano_aiu` is the request cost in billionths of a credit.
`input_tokens` already includes cache reads.

pulse reads the table through the `sqlite3` binary that macOS ships, in read-only mode.
This avoids a SQLite dependency in `go.mod`.
These rows cover the CLI only, so they add up to less than the account spend.
