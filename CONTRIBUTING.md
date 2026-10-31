# Contributing

Thanks for helping. Bug reports and feature requests go in
[GitHub issues](https://github.com/youssefKadaouiAbbassi/make-it/issues). For a change larger than a
small fix, open an issue first so we can agree on the approach.

## Build and test

The checks run in Docker, so nothing is installed on your machine. `cli/scripts/go.sh` runs a
command in the `golang` image with the repository mounted.

The Go checks, from `cli/`. All of them must pass with no output from gofmt, vet or staticcheck:

```console
$ cd cli
$ scripts/go.sh sh -c 'go generate ./... && test -z "$(gofmt -l internal tools cmd)" && go vet ./... && CGO_ENABLED=1 go test -race -count=1 ./...'
$ scripts/go.sh go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
$ scripts/go.sh go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
$ scripts/go.sh sh -c 'go mod tidy -diff && go mod verify'
```

To build and run the CLI:

```console
$ scripts/go.sh go run ./cmd/makeit help
```

The other suites, from the repository root:

```console
$ bash ci/test.sh                      # the CI checks makeit writes into projects (Docker)
$ bash plugins/makeit/hooks/test.sh    # the plugin's hooks, with fake payloads (needs bash, jq, git, realpath)
$ bash cli/scripts/test-install.sh     # install.sh against a fake signed release (Docker)
```

The pull request CI runs the Go checks. Run the other suites yourself when your change touches
`ci/`, `plugins/makeit/hooks/` or the install scripts.

Tests for a bug fix must fail without the fix and pass with it.

## Commits

- One logical change per commit.
- Subjects in the imperative mood: "Add the gopls row", not "Added" or "Adds".
- No AI attribution lines: no `Co-Authored-By:` line for an AI tool, and no "Generated with" line,
  in commits or pull request descriptions.
- Never commit secrets.

## Pull requests

Open them against `main`. The Go checks must pass.
