<p align="center">
  <a href="https://spun.ink">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset=".github/assets/spun-ink-logo-dark.svg">
      <img src=".github/assets/spun-ink-logo.svg" alt="spun.ink" width="360">
    </picture>
  </a>
</p>

# spun — the spun.ink command line

Operate a [spun.ink](https://spun.ink) website from the shell. spun.ink hosts websites that are
run entirely by an AI agent; `spun` gives that agent (or you) the site's tools as commands. Every
command is one call to the site's MCP server (`POST /mcp`), with the same token and the same tools an
MCP client sees, so files move by path instead of through the agent's context.

The server stays the only authority on what a tool does. This client adds login, profiles, file
arguments, checksummed uploads and JSON output; the code is public so you can check what it does
with a token that can change your live site.

## Install

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.ps1 | iex
```

Both download the latest release, check it against its `checksums.txt` and install `spun` for
your user only — `~/.local/bin`, or `%LOCALAPPDATA%\Programs\spun` on Windows. `SPUN_VERSION`
picks a release, `SPUN_INSTALL_DIR` the directory. Read the scripts first if you like:
[install.sh](scripts/install.sh), [install.ps1](scripts/install.ps1).

**By hand** — download an archive for macOS, Linux or Windows (amd64, arm64) from the
[releases](https://github.com/spun-ink/cli/releases) page and check it:

```bash
shasum -a 256 -c checksums.txt --ignore-missing
```

The macOS binary is not notarized yet. An archive downloaded with a browser is quarantined, and
macOS refuses to open it until you run `xattr -d com.apple.quarantine spun` once; the install script
does not need that.

Every archive carries an SPDX software bill of materials (`*.sbom.json`).

### Staying current

```bash
spun upgrade --check                 # the running and the latest version; changes nothing
spun upgrade                         # to the latest release
spun upgrade 1.4.0                   # to a named release, prereleases included
```

`spun upgrade` replaces the binary it runs from, when that is a release installed into your home
directory (by the install scripts, or an archive unpacked there). The download is checked against
the release's `checksums.txt`, as the installers do; the old binary stays until the new one answers
from its place, and is put back if it does not. It never downgrades, never runs as root or
administrator, and needs no login. Any other install exits 5 (`upgrade_required`) with the command
that updates it — `go install github.com/spun-ink/cli/cmd/spun@latest` for a `go install` build.

After a command, a release build checks at most once a day whether a newer release exists, and
says so on stderr — only when both stdout and stderr are a terminal, so an agent or a script never
sees it (they ask `spun upgrade --check`). It never installs anything. `CI` or
`SPUN_NO_UPDATE_CHECK=1` turn it off.

The skill follows the binary: after an upgrade or a reinstall, the next command rewrites `SKILL.md`
wherever `spun setup` put it at an agent's standard location (`~/.claude`, `~/.codex` or
`$CODEX_HOME`). A skill installed with `spun setup --dir` is refreshed by rerunning that setup, and an
agent session that has already loaded the skill keeps the old text until it restarts.

## Getting started

1. **A new account** — sign up from the shell:

   ```bash
   spun signup                        # asks for email, name and site handle
   spun signup --email you@example.com --name "Rosa's Bakery" --handle rosas-bakery
   ```

   It shows the terms sentence spun.ink publishes (with its URLs) and asks you to accept it before
   anything is sent; without a terminal — an agent's shell — it needs `--accept-terms`, passed only
   once the human has read that sentence. The token the server returns is stored, never printed.
   Then confirm the email we send: pressing its button concludes the contract and puts the site
   online. `signup` refuses when `spun.ink` is already stored — a second sign-up is a second account.

   **An existing account** — log in. The token is read from a no-echo prompt (or stdin), never from
   an argument, so it never lands in shell history. Lost it? https://spun.ink/recover.

   ```bash
   spun login
   ```

   Either way the token goes to the OS credential store (macOS Keychain, Windows Credential Manager, the Linux
   Secret Service). A Linux or macOS machine without one falls back to an owner-only file
   (`0600`) in `~/.config/spun/`; on Windows there is no fallback — a token is never kept in a
   plain file there.

   Both also install the `spun` skill for every agent it finds (`~/.claude`, `~/.codex` or
   `$CODEX_HOME`). `--no-setup` skips that; `spun setup` reruns it, `spun setup --remove` undoes it.

2. Ask your agent to work on your site — or look around yourself:

   ```bash
   spun tools                         # every tool with a one-line summary
   spun help update_content           # one tool's description and input schema
   spun call site_map
   ```

## Servers and profiles

A command that names no server goes to **https://spun.ink — the live site**. Another server (a
development server, for instance) gets its own profile and is named on every command:

```bash
spun login --profile dev --url http://spun.localhost:3002
spun --profile dev tools              # or: export SPUN_PROFILE=dev
```

- The profile name `spun.ink` only ever means https://spun.ink, so another URL can never become the
  default.
- A stored token is bound to the server it was stored for. Moving a profile to another server
  stores a new credential; `SPUN_URL` can never send a profile's token elsewhere.
- `SPUN_URL` with `SPUN_TOKEN` works without a profile; `SPUN_TOKEN` alone goes to spun.ink.
- Plain `http://` is accepted for `localhost` only.

`spun profiles` lists what is stored (never a token); `spun logout` forgets `spun.ink`,
`spun logout --profile <name>` another.

## Arguments

`key=value` is a string · `key:=json` is raw JSON · `key=@path` is a file's contents ·
`key:=@path` is a JSON file.

```bash
spun call update_content slug=about title="About us"
spun call update_block id:=42 data:=@hero.json
spun template pull hero > hero.liquid
spun template push hero hero.liquid
spun asset upload photo.jpg --alt "The team"
```

## Output and exit codes

At a terminal output is readable; piped, stdout is JSON. Errors go to stderr as
`{"ok":false,"error":{"code","message"}}`.

| exit | meaning |
|---:|---|
| 0 | ok |
| 1 | the tool refused — the body says why |
| 2 | usage: unknown command, malformed argument, unknown tool or invalid params |
| 3 | unauthorized: no token, or the server rejected it |
| 4 | network or protocol failure, or a failed upgrade (`upgrade_failed`, `upgrade_rolled_back`, `upgrade_broken` — the message says what is installed now) |
| 5 | no server named, local configuration missing, or this install cannot upgrade itself (`upgrade_required`) |

## Documentation

The platform: https://spun.ink/docs. The authoring manual for agents: `spun call capabilities`.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

## Licence

MIT — see [LICENSE](LICENSE). © 2026 City of Code GmbH.
