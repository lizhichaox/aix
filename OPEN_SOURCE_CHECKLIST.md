# Open-Source Publication Checklist

This checklist is the final gate before changing the GitHub repository from
private to public.

## Rights and licensing

- [x] Add the chosen project `LICENSE` file (Apache-2.0).
- [x] Confirm that every contributor has the right to license their commits.
- [x] Replace the redistributed upstream catalog and instruction text with
  AIX-owned fallback metadata and original Apache-2.0 instructions.
- [x] Publish from a fresh root commit so removed upstream assets and the
  alternate maintainer email are not retained in public Git objects or tags.
- [x] Retain direct dependency license texts under `LICENSES/`; require release
  archives to include them and `THIRD_PARTY_NOTICES.md`.

## Repository review

- [x] Review every tracked file and the complete Git history for private data.
- [x] Run Gitleaks against all refs; no leaked credentials were detected.
- [x] Confirm that author identities belong to the maintainer; `.mailmap`
  canonicalizes the alternate account as `lizhichao <x@lizhichao.com>`.
- [ ] Confirm that no private branches, tags, releases, issues, Actions logs,
  or repository secrets contain material that should not become public.
- [x] Verify that the version in `cmd/root.go` matches the top changelog entry.

## Verification

- [x] `test -z "$(gofmt -l .)"`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go build ./...`
- [x] `git diff --check`
- [x] `gitleaks git --redact .`
- [x] `govulncheck ./...`
- [ ] CI passes on macOS and Linux with Go 1.23 and the latest stable Go.

## GitHub settings

- [ ] Set the description, website, topics, and social preview.
- [ ] Enable private vulnerability reporting, secret scanning, and push
  protection where available.
- [x] Add a least-privilege CodeQL workflow for Go.
- [ ] Protect `main`: require pull requests, CI, resolved conversations, and
  block force pushes and branch deletion.
- [ ] Limit Actions permissions to read-only by default and allow write access
  only to workflows that require it.
- [ ] Review deploy keys, webhooks, collaborators, environments, and Actions
  secrets before publication.
- [ ] Enable Dependabot alerts and security updates.

## First public release

- [ ] Create a release commit through the protected branch.
- [ ] Create an annotated tag matching the source version.
- [ ] Publish macOS and Linux `amd64`/`arm64` archives with SHA-256 checksums.
- [x] Add a tag-triggered release workflow that packages required license
  material and publishes checksums.
- [ ] Verify one downloaded archive and perform a clean install/restore smoke
  test.
- [ ] Publish the repository only after all earlier checks are complete.
