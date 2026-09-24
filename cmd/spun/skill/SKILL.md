---
name: spun
description: >
  Operate a Spun website (spun.ink) from the shell with the `spun` CLI — edit templates, pages,
  posts, navigation, forms and assets, preview and publish. Use whenever the task changes or reads
  a Spun site and a shell is available.
---

# spun

Spun hosts websites that are operated entirely by an agent. A site is data — settings, templates,
content, navigation — and every change goes through the site's MCP tools. The `spun` CLI calls
those same tools over the same token, one command per call, so files move by path instead of
through your context.

## Pick the server first

A command that names no server goes to **https://spun.ink, production**, with the token
`spun login` stored as profile `spun.ink`. Whether a write there reaches anyone depends on whether
the site is public yet — see below.

Another server is named with:

- `--profile <name>` (or `SPUN_PROFILE=<name>`) — a server stored by `spun login --profile <name> --url <server>`
- `SPUN_URL` plus `SPUN_TOKEN`, without a profile (a profile's token never goes to another server)

Run `spun profiles` first. Only `spun.ink`: work there. Other profiles too (a dev server): ask the
owner which one this task is for, and pass it by name.

## Not logged in yet — the owner logs in, not you

Exit 5 with "not logged in" means no token is stored. The token is the owner's key to their
account. **Never ask for it in chat, never write it to a file, never pass it as an argument.** Ask
the owner to run the login themselves, in their own terminal — it prompts for the token without
echo, so you never see it:

```bash
spun login
```

If the owner already has an account, **do not sign up again** — a second sign-up is a second,
separate account; a lost token is replaced at https://spun.ink/recover.

No account yet: the owner is best served running `spun signup` in their own terminal — it asks for
email, name and site handle, shows the terms and asks them to accept. From your shell instead, run
it once without `--accept-terms`: it refuses (exit 2) with the terms sentence and its URLs. **Show
your human that sentence verbatim and wait for their yes** — the contract is theirs, not yours —
then:

```bash
spun signup --email <theirs> [--name <name>] [--handle <handle>] --accept-terms
```

The token is stored as profile `spun.ink`, never shown. Tell the owner to press the button in the
confirmation email; the site stays under construction until they do. `validation_failed` (exit 1,
e.g. a taken handle) means pass another `--handle`. Exit 3 means the server rejected the token: send the owner to `/recover`,
do not retry with guesses.

## Learn a tool before you call it

```bash
spun tools                 # every tool with a one-line summary
spun help <tool>           # one tool's description and input schema
```

Run `spun help <tool>` before a tool you have not used. The server is the only authority on what a
tool takes; `spun call` sends whatever you pass.

## Arguments

`key=value` is always a string · `key:=json` is raw JSON · `key=@path` is a file's contents ·
`key:=@path` is a JSON file. Only the first `=` separates; a string starting with `@` goes as JSON:
`handle:='"@spun"'`.

```bash
spun call get_content slug=about
spun call update_content slug=about title="About us" meta_description="Who we are"
spun call update_block id:=42 data:=@hero.json
```

`--site <handle>` targets one site of an account with several.

## A site nobody can see yet

A new site is not public. Its root shows an under-construction page, and every other path answers
404, while **any** of these holds:

- the owner has not yet pressed the button in the confirmation email (`spun call get_account`
  shows it)
- the site has no layout template (`update_site layout_template=<key>` wires one)
- no home page is published

Until then no write has an audience: build templates, settings and the first pages freely, and
hand the owner a `create_preview_link` URL to look at. The deploy discipline below starts the
moment the site is public.

## Keeping spun current

`spun upgrade --check` reports `{current, latest, available}` and changes nothing. To update, run
`spun upgrade` as its own command and say so first: it replaces the binary you are calling. Exit 5
`upgrade_required` means this install cannot update itself — pass the command in its message on to
the owner. This file is rewritten on the next command after an upgrade, but you keep the text you
already loaded: suggest a new session.

## One write per command

Run each write as its own shell command, never chained with `&&` or `;`, and say what it changes
before you run it. A reviewer — the owner, or a permission check — can approve one small command it
understands; a chain of four writes reads as one opaque deploy and gets refused.

## Two kinds of write — know which one you are making

**Only content has a draft.** Pages, posts and their blocks stay draft until `publish_content`;
everything else is public the moment the write commits. Decide which kind a change is before you
run a command.

### Content: draft, preview, then publish

```bash
spun call update_content slug=home title="Welcome"   # 1. edit the draft
spun call create_preview_link                       # 2. preview — hand the URL to the owner
spun call publish_content slug=home                 # 3. publish, once the owner approves
```

### Live on write: templates, settings, navigation, forms, records

`spun template push`, `update_site`, `set_nav`, `set_form` and `set_record` change the public site
immediately. There is no draft and no preview — a preview link renders the same live template.
Treat each one as a deploy:

```bash
spun template pull hero > hero.orig.liquid           # 1. pull what is live
cp hero.orig.liquid hero.liquid                      # 2. edit the copy with your own file tools
diff hero.orig.liquid hero.liquid                    # 3. show the owner the change, get a yes
spun template push hero hero.liquid                  # 4. push — live now
```

The push is render-checked, so broken Liquid is refused; an unwanted design is not. Every live
write records the state it replaced: `spun call list_revisions type=template` (or `type=site` for
settings, navigation and forms) and `restore_revision` undo it. To try a template out safely, create
it under a new key nothing references yet, use it from draft content, and preview that —
`spun call capabilities section=authoring_loop` has the full swap procedure.

Files never pass through the model: `spun asset upload photo.jpg --alt "…"` sends the bytes and
verifies their sha256.

Bulk work composes:

```bash
spun content list --kind page --status draft --ids-only | xargs -I{} spun call publish_content slug={}
```

## Output and exit codes

Piped, stdout is JSON; errors go to stderr as `{"ok":false,"error":{"code","message"}}` — the same
vocabulary the MCP tools use, so read `error.message` and correct the call.

| exit | meaning |
|---:|---|
| 0 | ok |
| 1 | the tool refused — the body says why |
| 2 | usage: unknown command, malformed argument, unknown tool or invalid params — fix the call, do not retry |
| 3 | unauthorized: no token, or the server rejected it |
| 4 | network or protocol failure — a redirect names the server to use instead |
| 5 | no server named, or local configuration missing |

`spun call capabilities` is the full authoring manual; `spun call capabilities section=<name>`
reads one chapter.

The platform's documentation, including this CLI: https://spun.ink/docs#cli. The source and
releases: https://github.com/spun-ink/cli.
