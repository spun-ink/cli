# Contributing

Thank you for helping. `spun` is deliberately small: a thin client over the spun.ink MCP server.
A change that belongs in the server (what a tool does, what it accepts, its error wording) is
better raised as an issue here, and we carry it over.

## Before you start

Open an issue first for anything beyond a small fix, so we can agree on the shape before you
write code. Security problems go through [SECURITY.md](SECURITY.md), never a public issue.

## Development

Go 1.27 or later. The code lives in `cmd/spun`.

```bash
gofmt -l .                    # must print nothing
go vet ./...
go test -race ./...
go build -o spun ./cmd/spun
```

CI runs the same on Linux, macOS and Windows, plus `govulncheck`. Tests never reach a real server:
they use an in-process fake and a mocked keyring.

Keep to what is there:

- No new dependency without a reason in the pull request.
- A token never appears in an argument, a log line, an error message or a file other than the
  credential store.
- The server's error body passes through untouched; the CLI adds codes only for failures the
  server never saw (usage, configuration, network).

## Pull requests

One change per pull request, with a test that fails without it. By contributing you agree that
your contribution is licensed under the MIT licence of this repository.
