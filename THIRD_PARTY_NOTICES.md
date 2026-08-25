# Third-Party Notices

Except for the third-party material identified below, AIX is licensed under
the Apache License 2.0 as provided in `LICENSE`.

AIX depends on the Go modules recorded in `go.mod` and `go.sum`. Their authors
retain their respective copyrights, and their own license terms apply.

The modules linked into supported builds are:

| Module | Version | License |
| --- | --- | --- |
| `github.com/BurntSushi/toml` | v1.6.0 | MIT |
| `github.com/spf13/cobra` | v1.10.2 | Apache-2.0 |
| `github.com/spf13/pflag` | v1.0.9 | BSD-3-Clause |

`github.com/inconshreveable/mousetrap` v1.1.0 (Apache-2.0) is linked into
Windows builds, which are not currently a supported AIX distribution target.
The corresponding license texts are retained under `LICENSES/` and must be
included in release archives.

## DeepSeek runtime catalog

AIX can fetch model metadata at runtime from DeepSeek's official Codex setup
script:

<https://cdn.deepseek.com/api-docs/codex-deepseek-setup.sh>

DeepSeek retains any rights it holds in the fetched material. AIX does not
bundle or redistribute a copy of the script or its model catalog. When the
runtime fetch is unavailable, AIX generates fallback metadata from its own
provider registry.

## Codex base instructions

`internal/assets/codex_base_instructions.txt` contains AIX's original
Apache-2.0-licensed base instructions used to construct an interoperable model
catalog. It does not contain the instruction text published in DeepSeek's
setup script.
