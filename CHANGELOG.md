# Changelog

## v0.11.5 (2026-08-26)

- Added `aix usage [provider]` for native Codex and Claude allowances plus
  OpenCode Go subscription windows, DeepSeek balances, and OpenRouter key
  spending. The command can query one provider or aggregate every configured
  provider without switching, persisting, or estimating usage.
- Added a short-lived on-disk cache for `aix usage`, defaulting to 60 seconds
  and controlled by `--ttl`, so repeated polling returns provider-reported
  snapshots without re-querying and avoids provider-side rate limits.
- Made `aix usage codex` read the native ChatGPT allowance regardless of the
  active provider, and report Claude's plan from its saved subscription login.

## v0.11.4 (2026-08-26)

- Enabled Claude Desktop Auto mode for managed third-party providers, avoiding
  the warning and persisted downgrade to Manual when switching from native
  Anthropic operation.
- Preserved Codex's native model, reasoning effort, authentication, provider
  definitions, and model catalog across managed provider switches and restored
  them without overwriting unrelated configuration changes.
- Hardened the Codex native snapshot lifecycle with first-switch retention,
  atomic private writes, format validation, and restore-time consumption.

## v0.11.3 (2026-08-26)

- Read Claude's native top-level model selection and report documented context
  windows for current Anthropic family aliases.
- Read native Codex context windows from its server-refreshed model cache
  instead of reporting zero, and omit unknown JSON context values.
- Correctly distinguish real Codex Desktop user agents from Codex CLI traffic
  and add real-world Claude Desktop/Code client-classification coverage.

## v0.11.2 (2026-08-26)

- Recorded successful provider/model/effort switches in the gateway log so
  route changes are visible before the first request uses them.
- Moved the public harness identity to a prominent `[claude]` or `[codex]`
  prefix on request, routing, model-rewrite, response, and request-error logs.
- Added best-effort client classification for Claude Code, Claude Desktop,
  Codex CLI, and Codex Desktop while keeping ambiguous traffic explicit.

## v0.11.1 (2026-08-25)

- Preserved Codex conversation-history lineage: paginated rollouts are kept
  byte-for-byte and reasoning/session contents are no longer rewritten on
  provider switch or restore, resolving the "cutoff byte offset past the
  source rollout" resume error.
- Pinned Codex Responses reasoning effort to the configured value and unified
  status mode reporting to `gateway`, so `aix status` reflects what requests
  actually use.
- Stopped logging no-op model rewrites; the gateway now logs only real
  model-to-upstream mappings.
- Made gateway readiness trust the `/health` endpoint when the PID file is
  stale or unreadable, removing a false "AIX gateway is not running" warning
  in managed status output.
- Expanded `aix log` with route rendering, provider filtering, line limits,
  and follow mode, and added provider administration helpers for gateway-key
  migration and write-safe proxy config loading.

## v0.11.0 (2026-08-25)

- Routed managed Codex providers through the private AIX gateway using native
  Responses passthrough, isolated provider routes, gateway authentication,
  model rewriting, SSE streaming, and route-aware request logs. Restoring
  Codex continues to use its OpenAI-native direct connection.
- Added active model and context length to status output, made gateway logs
  show the harness/provider/model/effort relationship, and fixed Claude
  Desktop to prefer provider models that advertise one-million-token context.
- Added cross-platform CI, security and contribution policies, issue and pull
  request templates, automated dependency updates, CodeQL analysis,
  third-party provenance notices, Apache-2.0 licensing, automated release
  archives and checksums, release checks, and public installation, privacy,
  and uninstall documentation.
- Canonicalized historical commits from the maintainer's alternate account
  through `.mailmap`.
- Replaced redistributed upstream model-catalog content with generated AIX
  fallback metadata and original Apache-2.0 base instructions; live DeepSeek
  metadata remains a runtime-only refresh.
- Reduced the public CLI to five root commands: `claude`, `codex`, `setup`,
  `status`, and `log`; root help is now 21 lines.
- Unified Claude Code and Claude Desktop as one harness. Provider switches and
  native restore now always update both clients together.
- Limited Claude and Codex subcommands to `restore` and `restart`, with the
  same `--list`, `--edit`, `--doctor`, and `--effort` mapping flags.
- Removed the web dashboard, DSH integration, session browsing, shell command
  executor, public provider/proxy administration, all usage collection and
  persistence, completion, self-install, compatibility aliases, and other
  non-core command paths.
- Made the Claude gateway private infrastructure managed automatically by
  setup, provider switches, and the installer.
- Made setup non-interactive: missing credentials are reported and skipped,
  with a prominent warning when no provider credential is configured.

## v0.10.1 (2026-08-25)

- Added harness-specific provider/model registries for Codex and Claude, with
  editable defaults, model validation, diagnostics, and default effort
  resolution.
- Standardized the primary CLI shape as
  `aix <harness> <provider> [model] [effort]` while preserving compatibility
  subcommands.
- Set the bundled model default to DeepSeek V4 Flash Vision Exp and the
  bundled effort default to `high`.
- Added verified OpenCode Go Anthropic Messages mappings for DeepSeek V4 Pro,
  DeepSeek V4 Flash, and DeepSeek V4 Flash Vision Exp.
- Improved Claude Desktop model presentation with correct default-tier
  selection, distinct standard/1M labels, and a DeepSeek-only default picker
  for OpenCode Go.
- Made the active proxy gateway key authoritative for Claude Desktop and
  migrated legacy default keys to `aix-claude-gateway-api-key` without
  changing custom keys.

## v0.10.0 (2026-08-19)

- Renamed the project from ATS to AIX.
- Simplified provider switching: `aix codex <provider> [model]` and
  `aix claude <provider> [model]` now switch directly; use `--list` to inspect
  models.
- Codex now uses native Responses API providers only. Protocol conversion was
  removed, and the proxy is limited to authentication, model rewriting, and
  usage tracking.
- Claude Code and Claude Desktop now support native Anthropic providers,
  including DeepSeek, OpenCode Zen, OpenCode Go, and OpenRouter.
- Added provider registry support, user-defined native providers, model
  catalog synchronization, and Codex desktop model/reasoning metadata.
- Fixed Claude Desktop third-party gateway activation and restart behavior.
- Added `aix dsh status|start|restart|update` for the DeepSeek Harness stack.
- Added sandbox-safe `aix exec`, working-directory forwarding, and proxy
  liveness checks.
- Added 1M-context model metadata and updated DeepSeek V4 Pro support.

## v0.9.4 (2026-08-06)

- Added OpenCode Zen, OpenCode Go, and OpenRouter as native Codex providers.
- Added Codex session listing and history synchronization after provider
  switches.

## v0.9.0–v0.9.3 (2026-08-05)

- Added the local web dashboard and shared provider administration.
- Added Claude provider switching and model mapping through the AIX proxy.
- Added native catalog validation and completed the module-path migration.

## v0.8.25–v0.8.45

- Added native provider registry and user-defined providers.
- Improved setup, unset, restore, backup, restart, and proxy lifecycle
  behavior.
- Added usage/session tracking improvements, provider model management, and
  Claude Desktop 3p support.
- Hardened streaming, routing, authentication, body-size limits, and error
  handling.

## v0.8.0–v0.8.24

- Added the unified CLI command surface, completion, status, diagnostics,
  session management, and proxy controls.
- Added launchd/systemd support, streaming usage tracking, session metadata,
  and optional upstream HTTP proxy support.
- Improved installation, self-update, graceful shutdown, and security checks.

## v0.6.2–v0.7.5

- Initial CLI and reverse-proxy implementation with provider routing,
  Responses/Chat Completions support, launchd service integration, session
  tracking, setup wizard, and diagnostics.
