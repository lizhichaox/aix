# Contributing to AIX

Thank you for helping improve AIX.

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Before opening a change

- Search existing issues and pull requests before starting substantial work.
- Open an issue first for changes to the public command surface, supported
  harnesses, protocol behavior, or configuration lifecycle.
- Keep code, comments, documentation, commit messages, and CLI output in
  English.
- Never include real credentials, user configuration, logs, or session data in
  an issue, test fixture, commit, or pull request.

## Development

AIX requires Go 1.23 or later.

```bash
go build -o aix .
go vet ./...
go test ./...
```

Some tests open localhost listeners. Run them in an environment that permits
loopback networking.

Before submitting a pull request, also run:

```bash
gofmt -w .
git diff --check
```

## Pull requests

- Keep each pull request focused on one change.
- Add or update tests for behavior changes.
- Update the README or changelog when the public behavior changes.
- Describe any configuration migration, credential handling, or session-data
  impact explicitly.
- Do not commit generated binaries.

By contributing, you certify that you have the right to submit the work under
the project's license and agree to the Developer Certificate of Origin 1.1.
Add a `Signed-off-by` line to each commit with `git commit -s`.

The Developer Certificate of Origin is available at
<https://developercertificate.org/>.
