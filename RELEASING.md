# Releasing AIX

Only maintainers should perform releases. Release actions must be deliberate;
the local `make release` target installs a build on the current machine and is
not the public distribution workflow.

The first public release must also satisfy every item in
[`OPEN_SOURCE_CHECKLIST.md`](OPEN_SOURCE_CHECKLIST.md).

## Preparation

1. Confirm that the repository license, third-party notices, and contributor
   permissions cover all material in the release.
2. Confirm that the version in `cmd/root.go` matches the top entry in
   `CHANGELOG.md`.
3. Review dependency updates and public security advisories.
4. Scan the complete Git history for credentials.

## Verification

Run from a clean worktree:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go build ./...
git diff --check
gitleaks git --redact .
govulncheck ./...
```

Tests must run in an environment that permits localhost listeners. Verify the
CLI manually on every supported operating system before a stable release.

## Publication

1. Merge the release change through the protected default branch after CI
   succeeds.
2. Create an annotated `vX.Y.Z` tag matching the source version.
3. Push the annotated tag. The release workflow builds macOS and Linux
   archives for `amd64` and `arm64`, includes `LICENSE`, `LICENSES/`, and
   `THIRD_PARTY_NOTICES.md`, publishes SHA-256 checksums, and creates the
   GitHub release from the tag annotation.
4. Confirm every build and publish job succeeded.
5. Download one published archive, verify its checksum and version output, and
   perform a clean installation smoke test.

Do not publish from a worktree containing uncommitted changes. Do not reuse a
release tag; publish a new patch version for corrections.
