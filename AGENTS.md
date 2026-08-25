# AGENTS.md

## Project

AIX is a Go CLI for switching AI providers and models across AI harnesses.
Its current public harnesses are `claude` and `codex`. It is built with Cobra
and TOML. The current release version is defined in `cmd/root.go`.

## Product boundaries

- Preserve conversation history. Provider switching may update configuration
  and Codex history tags, but must not rewrite or delete session contents.
- Codex uses native Responses API providers directly. It never uses the AIX
  proxy for native provider mode.
- Claude Code and Claude Desktop are one harness: every provider switch applies
  to both. They use Anthropic-native providers through the private gateway.
- Public commands, help, status output, JSON, and documentation must expose
  Claude exactly once as the `claude` harness. `claudecode` and `desktop` are
  internal configuration targets only and must never appear as separate
  harnesses or status rows.
- The proxy performs authentication, provider/model routing, model rewriting,
  and SSE passthrough. It must not translate between
  Responses, Chat Completions, or Anthropic protocols. Responses requests to
  the proxy are rejected with a clear 501 error.
- Claude switches are atomic across Code and Desktop. Codex switches remain
  independent.
- AIX does not collect, persist, estimate, or display token usage or cost.
  Usage accounting belongs to the harness or provider.
- Do not add closed-source client integrations or mutate client session files.

All code comments, documentation, commit messages, and CLI output must be in
English.

## Build and verification

```bash
go build -o aix .
go vet ./...
go test ./...
```

For restricted environments, use a writable cache when the default Go cache
is unavailable:

```bash
GOCACHE=/tmp/aix-gocache go vet ./...
GOCACHE=/tmp/aix-gocache go test ./...
```

Useful local commands:

```bash
make rebuild
./aix status
./aix claude opencode-go --doctor
```

Tests may require localhost listeners and writes under `~/.aix`; distinguish
sandbox permission failures from code failures.

## Important files

- `internal/proxy.go` — proxy routing, auth, model rewriting, and streaming.
- `internal/apply.go` — per-app configuration writes, backups, restore, and
  native provider application.
- `internal/config.go` / `internal/paths.go` — AIX state and filesystem paths.
- `internal/providers.go` — provider presets and the single template generator.
- `internal/codex_deepseek.go` — native provider registry and Codex catalog.
- `internal/native_providers.go` — user-defined native providers.
- `internal/apps.go` / `internal/appinfo.go` — app registry and app-specific
  behavior.
- `internal/launchd.go` / `internal/systemd.go` — service installation.
- `cmd/setup.go` / `cmd/claude_switch.go` / `cmd/codex_deepseek.go` — setup and
  switching entry points.

## Provider and client rules

### Codex

- `aix codex <provider> [model] [effort]` accepts only providers in
  `NativeProviderSpecs()` plus user-defined entries from
  `~/.aix/native.toml`.
- Native application writes `~/.codex/config.toml` and
  `~/.codex/models.json`; it must not start or depend on the proxy.
- Validate `--model` through the provider registry. Do not add provider-specific
  branches when registry metadata can express the behavior.
- Catalog entries need `display_name`, `supported_reasoning_levels`, and
  `default_reasoning_level` so Codex desktop can show the model and effort
  picker. Keep `model_reasoning_effort` inside the supported set.
- The bottom-left Codex login label and the composer model label are different
  surfaces: the former comes from `model_providers.<active>.name`, the latter
  from catalog `display_name`.
- Provider switches and restore synchronize Codex history tags through the
  existing `sync-history` path and auto-restart the host app when applicable.

### Claude

- `aix claude <provider> [model] [effort]` always updates Claude Code and
  Claude Desktop together. Do not reintroduce per-client switch commands.
- `aix status` must emit one row/object per harness and aggregate Claude Code
  and Claude Desktop into a single `claude` entry. Keep a regression test for
  this public contract.
- Native status rows must use their well-known provider IDs (`anthropic` for
  Claude and `openai` for Codex) and fall back to `DefaultHarnessEffort` when
  the native configuration does not explicitly persist an effort.
- `aix claude restore` restores both clients. `aix claude restart` re-applies
  the active Desktop gateway configuration and restarts Claude Desktop.

#### Claude Code

- `ANTHROPIC_API_KEY` must remain in the settings `env` block. Current Claude
  Code versions use it to bypass an expired OAuth session before sending a
  request; localhost proxy requests bypass AIX auth.
- Keep `ANTHROPIC_BASE_URL` in the settings `env` block and preserve the
  existing local dummy-key format used by the implementation.

#### Claude Desktop third-party mode

- Current Claude Desktop builds use `~/Library/Application Support/Claude-3p/`
  and a config-library entry, not only flat gateway fields in the main config.
- `applyDesktop3pGateway` must update the applied library entry,
  `configLibrary/_meta.json`, and `deploymentMode = "3p"`.
- Use Anthropic-shaped aliases such as `claude-opus-5`; DeepSeek mappings are
  maintained by `SetDeepSeekClaudeMappings`.
- Never remove native Desktop credentials (`apiKey`, `primaryApiKey`, or
  `oauthTokens`) when removing AIX gateway fields.
- `desktop_native.json` is the temporary native-config snapshot. A managed
  Claude provider switch creates it and `aix claude restore` consumes it.
  Restore must also handle the `Claude-3p` directory.
- Desktop rewrites its config on quit. Restart order must remain quit →
  re-apply gateway config → launch, using the helpers in
  `cmd/app_restart.go`.

## Configuration lifecycle

- `internal/providers.go` is the only template-generation path. Provider
  switches must use it, including stale-template regeneration.
- `ApplyProviderWithModel` may create a missing template before reporting an
  unknown provider.
- Back up before mutation and prune backups only after a successful apply or
  restore. Never delete user session data.
- `setup` must apply configurations, not merely record state.
- `setup` and `install.sh` must never block on credential input. Report each
  missing credential and continue; if none are configured, print a prominent
  warning and exit successfully after initializing AIX.

## Native provider extensibility

`NativeProviderSpecs()` is the single built-in registry. A native provider
spec contains its ID, display name, environment variable, base URL, default
model, supported models, and optional catalog metadata. User-defined native
providers are merged from `~/.aix/native.toml` at runtime.

When adding a built-in provider, prefer one registry entry plus a preset in
`KnownProviders()` if Claude or proxy templates are also needed. Keep model
validation, catalog generation, template staleness checks, and restore logic
registry-driven.

## Private Claude gateway and diagnostics

- The gateway has no public lifecycle command. Setup installs its service and
  Claude provider switches start or reload it on demand.
- Native-only Codex operation must not be reported as a gateway failure.
- Mapping diagnostics are exposed only through
  `aix <harness> <provider> --doctor`.
- Provider routing for `/v1/messages` requires an Anthropic-compatible
  upstream; Chat Completions routing uses provider/model mappings and optional
  `/<provider>/v1/...` prefixes.
- `~/.aix/proxy.log` commonly contains `responses upstream status: 501` when a
  client incorrectly sends Responses traffic through the proxy.

## Command surface

- Public root commands are limited to `claude`, `codex`, `setup`, `status`,
  and `log`.
- Claude and Codex expose only `restore` and `restart` subcommands.
- Both harnesses expose only `--list`, `--edit`, `--doctor`, and `--effort`.
- Keep root help below 50 lines. Do not add compatibility aliases or public
  infrastructure commands without an explicit product decision.

## Release and change discipline

- Keep the version in `cmd/root.go` and the top changelog entry synchronized.
- Before handing off a release, run `git diff --check`, `go vet`, tests, and a
  clean build. Report environment-caused verification failures explicitly.
- Do not commit, tag, push, install, or restart a live service unless the user
  explicitly requests that release action.
