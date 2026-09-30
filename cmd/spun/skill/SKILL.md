---
name: spun
description: "Build and operate a spun.ink website (on spun.ink, a <handle>.myspun.ink address or the owner's domain): templates, pages, posts, navigation, forms, records and assets, from draft to preview to publish. Use whenever a task reads or changes a spun.ink site, through connected spun.ink tools or the spun CLI."
---

# spun

spun.ink hosts websites operated entirely by an agent. A site is data — settings, templates,
content, navigation — and every change is a call to its MCP tools at `https://spun.ink/mcp`, as a
connector in a chat or through the `spun` CLI in a terminal. The authoring rules come first, once;
then how each way in works.

## Authoring rules — every way in

### Orient before you write

Read `capabilities` — the authoring manual — once per session; `capabilities` with
`section: "<name>"` reloads one chapter, `authoring_loop` first. Call `get_account` for the owner's
email, plan and limits, and `site_map` for every page and post with its status. An account with
several sites takes a `site` handle on every site tool; without it you work on the primary site.

Before the first write, load the quality guide,
https://github.com/cityofcode-io/agent-operated-websites (`capabilities` names the same URL under
`links`), and run its §2 preflight: prove your browser tools run, answer its platform questions from
`capabilities` (`section: "authoring_loop"`), take a recovery point with `checkpoint` (`type: all`)
and record a baseline. Its checks decide when a build is done; its report goes to the owner with the
preview link. If you have no browser tool, or cannot fetch the guide, say so in that report and hand
the owner the preview link with what you could not check — never report a build as done on checks
you did not run.

### Two kinds of write — know which one you are making

**Only content has a draft.** Pages, posts and their blocks stay draft until `publish_content`;
everything else is public the moment the write commits.

- **Content — draft, preview, then publish.** Edit the draft, call `create_preview_link` (a signed
  URL, valid 24 hours, pass `slug` to open on that page), hand the URL to the owner and wait for
  their yes. Then `publish_content`. The public site renders the last published snapshot, so an
  edit to a published page changes nothing until the next publish. Read the publish `warnings`:
  `link_targets_draft` means a link renders empty until its target is published too.
- **Live on write — templates, settings, navigation, forms, records.** `update_template`,
  `update_site`, `set_nav`, `set_form` and `set_record` change the public site immediately. There is
  no draft, and a preview link renders the same live template. Treat each one as a deploy: say what
  changes, `checkpoint`, get the owner's yes, then write. Template writes are
  render-checked, so broken Liquid is refused with the template and line; an unwanted design is not.

To try a template out safely, create it under a new key nothing references yet, use it from draft
content, and preview that. `capabilities` with `section: "authoring_loop"` has the full swap.

### What has an undo point — and what does not

Draft edits and creates capture nothing: `checkpoint` before a rework. `publish_content` and
`checkpoint` capture the state as it stands; every changing live write
captures the state it replaces first. `list_revisions` (`type: page`, `post`, `template`,
`collection`, `record`, or `site` for settings, navigation and forms) → `get_revision` →
`restore_revision` is the undo; a restore checkpoints the current state first, so it is undoable
too. Restoring content restores the draft: the public page changes only at the next
`publish_content`. A delete keeps the key's history, so a restore is also undelete — and an undelete
of a page or post that was ever published is public at once, on its last published revision, not the
one you restored. Treat it as a live write (say so, get the yes), then `publish_content` to make the
restored state the public one.

### A site nobody can see yet

A new site is not public. Its root shows an under-construction page while **any** of these holds:

- no layout template is wired (`update_site` with `layout_template`) — every other path answers 404
- the account's email is not verified — every other path answers 404. Only an account made before
  sign-up by code can be in this state; `get_account` shows it, `resend_verification_email` re-sends
  the mail
- no home page is published and wired (`update_site` with `home_page`)

An account made by code or in the connect window is verified from its first minute. If the server's
instructions say the owner still has to click a verification email, `get_account`'s
`email_verified` is the truth — do not wait on a mail that was never sent. Until the site
is public, no write has an audience: build freely and hand the owner a preview link. The discipline
above starts the moment the site is public.

### One write at a time

Make each write its own call, and say what it changes before you make it. The owner can follow one
change at a time, and a refused write stops alone instead of taking a batch with it. Updates are
partial: an omitted field is unchanged, an explicit `null` clears it — send only what you change,
except list fields (a block's list of items), which are replaced whole: read, patch, resend the full
list.
Every refusal is structured: read `error.message`, correct the call, never guess.

## Which way in — by what you can see

Decide by your tool list and your machine, not by the app you think you are in:

- spun.ink tools in your tool list (`site_map`, `publish_content`, any prefix) → the connector rules.
- A `spun` binary, already installed, on a machine the owner keeps → the CLI rules.
- Both → the precedence section at the end.

A code sandbox (a chat app's code execution) is not a machine the owner keeps: it is thrown away,
and a token stored there is lost with it. In a sandbox, never install `spun`, never sign up, never
log in. No `spun` binary means the CLI rules do not apply — use the connected tools, or tell the
owner to add the connector.

## Connected as a connector

This is you with spun.ink added as a connector — in Claude, ChatGPT, another chat app, or Claude Code
through this plugin.

- **Use the connected spun.ink tools only.** Find them in your tool list — the app may put a
  connector prefix in front of each name; match on the tool name (`site_map`, `publish_content`).
  Do not assume one namespace.
- **Never run CLI setup, never ask for a bearer token** — and in a code
  sandbox never install `spun`, sign up or log in with it. The owner's account is created or opened
  in the spun.ink sign-in window when they connect; no token ever enters the chat. If a token is pasted anyway, tell the owner to open https://spun.ink/recover and replace it.
- **The tools are missing?** Tell the owner to add the connector. In Claude: Settings → Customize →
  Connectors → Add custom connector, name `spun.ink`, URL `https://spun.ink/mcp`, then choose
  **Sign in now** — not "No sign-in", which Claude marks as detected and which skips sign-in. They
  sign in or sign up in the window that opens.
- **Speak plainly.** In a chat app assume the owner is not a developer: show no Liquid, JSON or tool
  names unless they ask, and show them a preview link, never raw data. In a terminal, match the
  person in front of you.
- **Live writes:** describe the change, `checkpoint`, then ask — before the write, not after.
- **Files the owner holds** (dropped in the chat, on their phone): call `create_upload_link` with
  `browser: true`, hand the owner the URL, and once they say the file is in, read the outcome with
  `get_upload`. Confirm its filename and size with the owner before you place it; the sha256 is for
  you to compare against `get_upload`. A file on the web goes through `upload_asset` with `url`.
- `delete_account` and `empty_trash` are not offered on a chat connection, and an account made in
  the connect window has no bearer token. The owner first mints one at https://spun.ink/recover
  (**Replace your token**), then runs them from the `spun` CLI on their own machine (`spun login`,
  then `spun call delete_account confirm_email=…` or `spun call empty_trash confirm_handle=…`), or
  through a terminal agent holding that token.

## With the spun CLI

Only on the owner's own machine, or a terminal agent whose profile store outlives the session, and
only when `spun` is already installed — configured for the account and server this task is for, or
set up by the owner as below. A code sandbox never uses these rules (see "Which way in"). The CLI
calls the same tools over the same token, one command per call, so files move by path instead of
through your context.

### Pick the server first

A command that names no server goes to **https://spun.ink, production**, with the token
`spun login` stored as profile `spun.ink`. Another server is named with:

- `--profile <name>` (or `SPUN_PROFILE=<name>`) — a server stored by
  `spun login --profile <name> --url <server>`
- `SPUN_URL` plus `SPUN_TOKEN`, without a profile (a profile's token never goes to another server)

Run `spun profiles` first. Only `spun.ink`: work there. Other profiles too (a dev server): ask the
owner which one this task is for, and pass it by name.

### Not logged in yet — the owner logs in, not you

Exit 5 with "not logged in" means no token is stored. The token is the owner's key to their
account. **Never ask for it in chat, never write it to a file, never pass it as an argument.** Ask
the owner to run `spun login` in their own terminal — it prompts without echo, so you never see it.

If the owner already has an account, **do not sign up again** — a second sign-up is a second,
separate account; a lost token is replaced at https://spun.ink/recover.

No account yet (on the owner's own machine — never in a sandbox): the owner is best served running `spun signup` in their own terminal — it asks for
email, name and site handle, shows the terms, mails a code and asks for it. From your shell instead,
run it once without `--accept-terms`: it refuses (exit 2) with the terms sentence and its URLs.
**Show your human that sentence verbatim and wait for their yes** — the contract is theirs, not
yours; the code they read from their mail is the acceptance — then:

```bash
spun signup --email <theirs> [--name <name>] [--handle <handle>] --accept-terms
```

This mails the owner a code and exits **6** (not a failure), printing
`{"status":"code_sent","email":…}`. **Ask the owner for the code**, then finish — the pending
sign-up is remembered per profile and server:

```bash
spun signup --code <code> [--name <name>] [--handle <handle>]
```

The token is stored as profile `spun.ink`, never shown. The account is verified once the code is
accepted; there is no confirmation email to click. `invalid_code` (exit 1): wrong or expired code —
ask the owner again, or start over with `spun signup`. `validation_failed` (a taken handle): repeat
`--code` with another `--name`/`--handle`, no new code. `rate_limited` (exit 4): wait, then repeat
the same command. Exit 4 `signup_unavailable` means this server has no terminal sign-up: the owner
signs up at https://spun.ink/signup, then runs `spun login` in their own terminal.

### Learn a tool before you call it

`spun tools` lists every tool; `spun help <tool>` shows one tool's description and input schema.
Run it before a tool you have not used — the server is the only authority on what a tool takes, and
`spun call` sends whatever you pass.

### Arguments

`key=value` is always a string · `key:=json` is raw JSON · `key=@path` is a file's contents ·
`key:=@path` is a JSON file. `key:=null` clears a field; `key=null` sends the string `"null"`.
Only the first `=` separates; a string starting with `@` goes as JSON:
`handle:='"@spun"'`. `--site <handle>` targets one site of an account with several.

```bash
spun call get_content slug=about
spun call update_content slug=about title="About us" meta_description="Who we are"
spun call update_block id:=42 data:=@hero.json
```

One write per command: never chain writes with `&&` or `;`.

### Files stay on disk

A live template edit, as a deploy:

```bash
spun template pull hero > hero.orig.liquid           # 1. pull what is live
cp hero.orig.liquid hero.liquid                      # 2. edit the copy with your own file tools
diff hero.orig.liquid hero.liquid                    # 3. show the owner the change, get a yes
spun template push hero hero.liquid                  # 4. push — live now
```

`pull --schema hero.json` writes the field list next to the markup, and `push --schema hero.json`
sends both in one render-checked write. The schema is a full replace — a field left out stops
rendering, and the reply warns `schema_fields_removed`.

`spun asset upload photo.jpg --alt "…" --title "…"` sends the bytes and verifies their sha256; the
file never passes through you. A title tells near-identical variants apart in `list_assets`.

Bulk work composes — but only after the owner approved that exact list: print it with
`--ids-only`, show it, get the yes, then run:

```bash
spun content list --kind page --status draft --ids-only | xargs -I{} spun call publish_content slug={}
```

### Keeping spun current

`spun upgrade --check` reports `{current, latest, available}` and changes nothing. To update, run
`spun upgrade` as its own command and say so first: it replaces the binary you are calling. Exit 5
`upgrade_required` means this install cannot update itself — pass the command in its message on to
the owner. This file is rewritten on the next command after an upgrade, but you keep the text you
already loaded: suggest a new session.

### Output and exit codes

Piped, stdout is JSON; errors go to stderr as `{"ok":false,"error":{"code","message"}}` — the MCP
tools' vocabulary.

| exit | meaning |
|---:|---|
| 0 | ok |
| 1 | the tool refused — the body says why |
| 2 | usage: unknown command, malformed argument, unknown tool or invalid params — fix the call, do not retry |
| 3 | unauthorized: no token, or the server rejected it |
| 4 | network or protocol failure — a redirect names the server to use instead |
| 5 | no server named, or local configuration missing |
| 6 | sign-up code sent — ask the owner for it |

## Both available — precedence

Use the connected tools, unless the task needs files on disk — a template you edit as a file, an
upload from your disk, a bulk run — then use the CLI. Before you mix them, check they are the same
account on the same server: `get_account` on the connector, `spun call get_account` on the CLI. If
the emails or servers differ, stop and ask the owner which one this task is for. Never switch
account or server silently.

Documentation: https://spun.ink/docs. The CLI's source and releases: https://github.com/spun-ink/cli.
