---
paths:
  - "**/*.go"
  - "**/go.mod"
  - "**/go.sum"
---

# Go

Toolchain: the latest stable Go (go.dev/dl), `GOTOOLCHAIN=local` in CI so the version is the one installed. Static binaries with `CGO_ENABLED=0` unless a dependency needs cgo.

| purpose | tool |
|---|---|
| format | `gofmt -l .` (empty output) |
| vet | `go vet ./...` |
| lint | `staticcheck ./...` |
| tests | `go test -race -count=1 ./...` |
| vulnerabilities | `govulncheck ./...` |
| modules | `go mod tidy -diff` and `go mod verify` |

Any output from gofmt, vet or staticcheck fails the check. Table tests with the standard `testing` package; no assertion libraries. Every exported name has a doc comment. Errors are wrapped with `%w` and checked with `errors.Is` or `errors.As`, never compared as strings.
