# CLI reference (`artifactctl`)

`artifactctl` is a thin, scriptable client over the [HTTP API](http-api.md). It
is built to be a pleasure for humans and friendly to agents: interactive prompts
and progress bars when attached to a terminal, plain or JSON output otherwise.

## Configuration

The client resolves its server URL and token from three sources (later
overrides earlier):

1. **Config file** — `~/.config/artifactctl/config.yaml` (or
   `$ARTIFACTCTL_CONFIG`).
2. **Environment** — `ARTIFACTSD_BASE_URL`, `ARTIFACTSD_TOKEN`.
3. **Flags** — `--base-url`, `--token`, `--profile <name>`.

The config file supports named profiles for multiple servers:

```yaml
current: default
profiles:
  default:
    base_url: https://artifacts.example.com
    token: tok_...
  staging:
    base_url: https://staging.example.com
    token: tok_...
```

Select one with `--profile staging`.

> This client config is distinct from the **server**, which takes flags and env
> only ([Configuration](configuration.md)).

## Global flags

| Flag | Meaning |
|------|---------|
| `--base-url` | artifactsd server base URL. |
| `--token` | management API token. |
| `--profile <name>` | config profile to use. |
| `--output text\|json` | output format (`text` default; `json` for scripts/agents). |
| `-q`, `--quiet` | suppress non-essential output. |
| `-v`, `--verbose` | verbose output. |
| `--insecure` | skip TLS verification (dev only). |

Exit codes: `0` ok, `1` generic error, `2` usage, `4` auth, `5` not-found.

## Commands

### Collections

```
artifactctl collection create <name> [--public|--unlisted|--protected|--private]
                                     [--list] [--no-list]
                                     [--crawlable] [--index] [--no-index]
                                     [--expires <ts>] [--expires-in <dur>]
artifactctl collection list
artifactctl collection show <name>
artifactctl collection delete <name> [--force]
```

`--force` is required to delete a non-empty collection (its tree cascades).

### Files

```
artifactctl put <collection>/<path> <localfile>
        [--content-type <ct>] [--header k=v ...]
        [--public|--protected|--bearer-token <t>|--generate-bearer-token|--basic <user:pass>]
        [--expires <ts>] [--expires-in <dur>] [--no-index]
artifactctl put-tree <collection> <localdir>
        [--content-type-auto] [--public] [--no-index] [--header k=v ...]
        [--include glob] [--exclude glob] [--large-upload-threshold <bytes>]
artifactctl get <collection>/<path> [-o <localfile>]
artifactctl rm [-r] <collection>/<path>
```

- `put` infers content type from the file extension unless `--content-type` is
  given.
- `--generate-bearer-token` creates a random token and prints it **once**;
  `--bearer-token <t>` takes an explicit value.
- `put-tree` walks the directory, applies `--include`/`--exclude` globs, and
  uploads each file under `<collection>/<relative-path>`, switching to
  large-upload sessions for files over `--large-upload-threshold`. It shows
  progress on a TTY and prints a summary.

### Metadata

```
artifactctl meta get <collection>[/<path>]
artifactctl meta set <collection>[/<path>] [--visibility ...] [--expires ...]
                                           [--header k=v] [--crawlable]
                                           [--index] [--no-index]
```

`meta get` shows both the object's own and the resolved effective metadata.
A bare `<collection>` targets the collection's own metadata, a trailing slash
(`demo/docs/`) targets a folder (materializing it on `set`), and any other
path targets the file.

### Shares

```
artifactctl share create <collection>[/<path>]
        [--unlisted|--protected] [--bearer-token <t>|--generate-bearer-token] [--basic <user:pass>]
        [--expires-in <dur>] [--expires <ts>]
artifactctl share list <collection>
artifactctl share revoke <share-id>
```

Omit the `/<path>` to share the whole collection. `create` prints the
`/s/{share_id}/` URL.

### Listing & crawl

```
artifactctl tree <collection> [--path <p>] [--depth <n>]
artifactctl crawl <collection> [--path <p>] [--depth <n>]
```

`tree` is the management listing; `crawl` is the policy-filtered agent view.

### Tokens

```
artifactctl token create <name> [--scope ...] [--expires-in <dur>]
artifactctl token list
artifactctl token revoke <id>
```

`token create` prints the secret **once** — store it then. Available scopes:
`admin`, `collections:read`, `collections:write`, `shares:read`,
`shares:write`, `tokens:read`, `tokens:write`.

### Maintenance & version

```
artifactctl maintenance cleanup    # trigger a janitor pass now
artifactctl version
```

### Skill install

```
artifactctl skill install <agent> [--global]
```

Installs the [`SKILL.md`](../SKILL.md) agent skill (teaching an AI assistant
how to publish artifacts with this CLI or MCP tools) into a coding agent's
tooling, so it's picked up automatically in that agent's sessions. Supported
agents: `claude`, `codex`, `copilot`, `crush`, `opencode`, `hermes`.

By default it installs into the current project (the working directory);
`--global` installs into the agent's user-wide location instead (not
supported for `copilot`, which has no such location).

| Agent | Local path | Global path (`--global`) |
|-------|------------|---------------------------|
| `claude` | `.claude/skills/artifactsd/SKILL.md` | `~/.claude/skills/artifactsd/SKILL.md` |
| `hermes` | `skills/artifactsd/SKILL.md` | `~/.hermes/skills/artifactsd/SKILL.md` |
| `codex` | `AGENTS.md` | `~/.codex/AGENTS.md` |
| `opencode` | `AGENTS.md` | `~/.config/opencode/AGENTS.md` |
| `crush` | `AGENTS.md` | `~/.config/crush/CRUSH.md` |
| `copilot` | `.github/copilot-instructions.md` | — |

`claude` and `hermes` use a directory-per-skill convention with the same
`SKILL.md` frontmatter format, so the file is copied verbatim. The other
agents read a single shared instructions file, so the skill's body (with its
YAML frontmatter stripped) is written inside a marker-delimited block; the
rest of the file — including content you wrote yourself — is left alone, and
re-running `skill install` only updates that block.

## Recipes

Public, non-indexable report from a built site:

```console
$ artifactctl collection create quarterly-report --public --list --crawlable --no-index
$ artifactctl put-tree quarterly-report ./dist --content-type-auto
```

Private run with a short-lived share:

```console
$ artifactctl collection create run-8f31c2 --private --expires-in 14d
$ artifactctl put-tree run-8f31c2 ./outputs
$ artifactctl share create run-8f31c2 --unlisted --expires-in 48h
```

Protected PDF behind a bearer token:

```console
$ artifactctl put client-demo/report.pdf ./report.pdf \
    --content-type application/pdf --protected --bearer-token demo-token
```

Public assets with CORS and caching headers:

```console
$ artifactctl put-tree web-assets ./public --public \
    --header 'access-control-allow-origin=*' \
    --header 'cache-control=public, max-age=3600'
```

## Command → API mapping

| CLI | API |
|-----|-----|
| `collection create` | `POST /collections` |
| `put` | `PUT /collections/{c}/files/{path}` (or begin-upload for large files) |
| `put-tree` | many `PUT` / begin-upload |
| `get` | `GET /collections/{c}/files/{path}` |
| `meta get/set` | `GET` / `PATCH .../meta` |
| `share create/list/revoke` | `/collections/{c}/shares`, `/shares/{id}` |
| `tree` | `GET /collections/{c}/tree` |
| `crawl` | `GET /collections/{c}/crawl` |
| `token *` | `/tokens` |
| `maintenance cleanup` | `POST /maintenance/cleanup` |
