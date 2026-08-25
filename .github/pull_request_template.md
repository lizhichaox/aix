## Summary

Describe the user-visible change and why it is needed.

## Verification

- [ ] `gofmt -w .`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `go build ./...`
- [ ] `git diff --check`

## Safety and compatibility

- [ ] No real credentials, user configuration, logs, or session data are included.
- [ ] Conversation contents are not deleted or rewritten.
- [ ] Configuration migration and credential-handling effects are documented.
- [ ] Public CLI or documentation changes are included where applicable.
- [ ] Commits include a DCO `Signed-off-by` line.
