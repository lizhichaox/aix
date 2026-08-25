# AIX

AIX switches providers, models, and reasoning effort across AI harnesses.

```bash
aix claude opencode-go
aix codex opencode-go
```

AIX does not collect telemetry, token usage, or cost data. It preserves
conversation contents when switching providers.

The default model and effort come from the harness-specific registry at
`~/.aix/harnesses.toml`. AIX ships with
`deepseek-v4-flash-vision-exp` and `high` as the defaults.

## Installation

AIX requires Go 1.23 or later. The CLI and its private AIX gateway support
macOS and Linux. Claude Desktop integration is currently macOS-only. Windows
builds do not install a background gateway service and are not currently a
supported configuration.

```bash
git clone https://github.com/lizhichaox/aix.git
cd aix
./install.sh
```

The installer builds AIX, copies it to a directory on `PATH`, and checks for
provider credentials already present in the environment or AIX configuration.
It never prompts for missing credentials. To build without installing:

```bash
go build -o aix .
```

Before setup, export at least one provider credential:

```bash
export DEEPSEEK_API_KEY="..."
export OPENCODE_GO_API_KEY="..."
export OPENCODE_ZEN_API_KEY="..."
export OPENROUTER_API_KEY="..."
```

`OPENCODE_API_KEY` is also accepted as a shared fallback for OpenCode Zen and
OpenCode Go. `aix setup` reports missing credentials without prompting and
stores configured provider credentials in `~/.aix/proxy.toml` with restricted
file permissions.

## Commands

```text
aix
├── claude [provider] [model] [effort]
│   ├── restore
│   └── restart
├── codex [provider] [model] [effort]
│   ├── restore
│   └── restart
├── setup
├── status
└── log
```

Claude switches always update Claude Code and Claude Desktop together. Managed
Claude and Codex providers use a private AIX gateway that AIX starts and
reloads automatically. Claude traffic remains Anthropic Messages end to end;
Codex traffic remains Responses end to end. The gateway authenticates, routes,
rewrites model IDs, logs routing metadata, and passes streaming responses
through without translating protocols. `aix codex restore` returns Codex to
its default OpenAI-native direct connection.

Both harnesses support the same mapping flags:

```text
--list              Show the provider's model mapping
--edit              Edit harness/provider/model/effort mappings
--doctor            Validate the effective mapping
--effort <effort>   Use the default model with an explicit effort
```

Examples:

```bash
# Use provider defaults
aix claude opencode-go
aix codex opencode-go

# Select a model and effort
aix claude opencode-go deepseek-v4-pro high
aix codex openrouter deepseek/deepseek-v4-flash-vision-exp high

# Inspect and validate mappings
aix claude opencode-go --list
aix codex opencode-go --doctor

# Restore native APIs
aix claude restore
aix codex restore
```

## Harness registry

Claude and Codex have separate mappings because they require different API
formats. The effective registry is materialized only when edited:

```bash
aix claude opencode-go --edit
```

The file is stored at `~/.aix/harnesses.toml`. Each provider can define a
Claude mapping using Anthropic Messages and a Codex mapping using Responses.
Future harnesses can add their own mapping without changing the command shape.

## Runtime behavior

- Claude provider changes are applied to Code and Desktop as one operation.
- Claude Desktop is restarted automatically after a switch. Use
  `aix claude restart` to re-apply the active configuration manually.
- The AIX gateway is private infrastructure; setup and managed provider switches
  manage its service lifecycle automatically.
- Codex sends managed-provider Responses traffic through an isolated gateway
  route. `aix codex restore` bypasses the gateway and restores OpenAI native.
- Codex history tags are synchronized automatically after provider changes;
  conversation contents are never deleted or rewritten.
- Configuration changes are backed up under `~/.aix/backups/`.

The gateway listens on `127.0.0.1:2026` by default. AIX sends requests only to
the provider selected by the user. Provider APIs receive the request data
submitted by the harness; AIX itself has no telemetry or analytics service.

## Known limitations

- Some Anthropic-compatible upstreams, including the currently observed
  OpenCode Go route, may return `404` for `/v1/messages/count_tokens` while
  normal `/v1/messages` requests continue to succeed. AIX passes this endpoint
  through and does not fabricate or estimate token counts. A future fix should
  use an upstream-native counting endpoint when one becomes available without
  introducing protocol translation or local usage accounting.

## Configuration files

```text
~/.aix/harnesses.toml       Harness/provider/model/effort mappings
~/.aix/proxy.toml           Internal AIX gateway providers and credentials
~/.aix/state.toml           Active providers
~/.aix/providers/           Generated client templates
~/.aix/backups/             Configuration backups
~/.aix/proxy.log            AIX gateway log
~/.codex/config.toml        Codex configuration
~/.codex/models.json        Codex desktop model catalog
~/.claude/settings.json     Claude Code configuration
```

Claude Desktop configuration is stored under `~/Library/Application Support/`
on macOS. AIX may also install `~/Library/LaunchAgents/com.aix.proxy.plist` on
macOS or `~/.config/systemd/user/aix-proxy.service` on Linux.

## Uninstalling

Restore the harnesses before removing AIX so their native configuration is
re-applied:

```bash
aix claude restore
aix codex restore
```

Then unload and remove the background service for your platform.

macOS:

```bash
launchctl unload "$HOME/Library/LaunchAgents/com.aix.proxy.plist" 2>/dev/null || true
rm -f "$HOME/Library/LaunchAgents/com.aix.proxy.plist"
```

Linux:

```bash
systemctl --user disable --now aix-proxy
rm -f "$HOME/.config/systemd/user/aix-proxy.service"
systemctl --user daemon-reload
```

Finally, remove the installed `aix` binary. The `~/.aix` directory contains
credentials, state, logs, and backups; inspect it before deleting it. AIX does
not delete conversation data during uninstall.

## Security

Never include `~/.aix/proxy.toml`, logs, client configuration, or session files
in bug reports. Report suspected vulnerabilities through GitHub private
vulnerability reporting as described in [SECURITY.md](SECURITY.md).

CI, Dependabot, CodeQL, Gitleaks, and `govulncheck` form the project's security
checks. Confirmed vulnerabilities are prioritized for a fix. If a real issue
cannot be fixed promptly, this section will identify the affected versions,
impact, temporary mitigation, and advisory link until a patched release is
available. Tool deprecation notices and unavailable checks are tracked as
maintenance issues, not presented as product vulnerabilities.

Third-party material and its provenance are documented in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## License

Copyright 2026 AIX contributors. AIX is licensed under the
[Apache License 2.0](LICENSE). Third-party material remains subject to the
terms described in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Development

```bash
go build -o aix .
go vet ./...
go test ./...
```

Tests that open localhost listeners may require permission in restricted
environments.

See [CONTRIBUTING.md](CONTRIBUTING.md) before submitting a change.
Maintainer release checks are documented in [RELEASING.md](RELEASING.md).
The first-publication gate is tracked in
[OPEN_SOURCE_CHECKLIST.md](OPEN_SOURCE_CHECKLIST.md).
