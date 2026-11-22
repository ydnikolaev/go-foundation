| id | kind | stability | summary |
|---|---|---|---|
| adoption | module | experimental | track what a tool's releases still ask of the repositories using it |
| agentdocs | module | experimental | ship agent guides inside a CLI and install them into a repository |
| alert | primitive | experimental | send operational alerts without flooding the channel |
| avatar | primitive | experimental | give users a pick-one set of generated profile pictures |
| clock | primitive | experimental | test time-dependent code without sleeping |
| comments | primitive | experimental | rules of a one-level comment thread: reply attach, edit window, content caps, honeypot, delete to stub, added mentions |
| cron | primitive | experimental | schedule recurring jobs in a service process |
| edit-coalescer | primitive | experimental | send the latest data of each remote object at most once per window |
| gitfixture | primitive | experimental | stop Go tests running git from failing at cleanup, and fail the build on a raw git spawn |
| keyset | primitive | experimental | page a long list with an opaque cursor safe against inserts |
| moderation | primitive | experimental | member reports, hiding, warnings and mutes with an append-only log |
| own-avatar | primitive | experimental | an HTTP handler that serves only the signed-in user's own profile photo |
| reactions | primitive | experimental | one-per-user reaction rules on a target: idempotent put and remove, each answering the count and whether the user's is in it |
| safehttp | primitive | experimental | HTTP client for URLs you do not control: refuses private, loopback and metadata addresses, caps body and redirects, one lookup per dial |
| slogtest | primitive | experimental | capture the default slog logger in a test and decode its JSON lines |
| telegram-fake | primitive | experimental | fake Telegram Bot API over HTTP for tests: keeps each call's path and body, answers with one reply you set |
| testdb | primitive | experimental | give each Go database test its own Postgres database, copied from a template built once |
| tiptap | primitive | experimental | check rich-text editor JSON against an allowlist; render it as an email |
| tplfields | primitive | experimental | list the data fields a Go template reads as sorted dotted key paths, from its parse tree; refuses what it cannot follow |
