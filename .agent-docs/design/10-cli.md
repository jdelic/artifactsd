# 10 — CLI (`artifactctl`)

Implements SPEC §15. `artifactctl` is a thin client over the HTTP API via
`pkg/apiclient`. The CLI must be **a pleasure to use** (operator decision E2):
command structure with `spf13/cobra`, and polished interactive/visual UX with
the [charmbracelet](https://github.com/charmbracelet) libraries —
`lipgloss` (styling), `bubbletea`/`bubbles` (progress bars for `put-tree`,
spinners), and `huh` (interactive prompts when run without flags, e.g. picking
a collection or confirming a destructive delete). All such polish degrades
gracefully to plain output when stdout is not a TTY or `--output json` is set.

> Client-side config (below) is a convenience file for the human-facing CLI and
> is distinct from the **server**, which is flags+env only ([12](12-config.md)).

## 1. Configuration & auth

Resolution order (later overrides earlier):
1. Config file `~/.config/artifactctl/config.yaml` (or `$ARTIFACTCTL_CONFIG`).
2. Env: `ARTIFACTSD_BASE_URL`, `ARTIFACTSD_TOKEN`.
3. Flags: `--base-url`, `--token`, `--profile <name>`.

Config supports named profiles (multiple servers):
```yaml
current: default
profiles:
  default:
    base_url: https://artifacts.example.com
    token: tok_...
```

Global flags: `--base-url`, `--token`, `--profile`, `--output text|json`,
`-q/--quiet`, `-v/--verbose`, `--insecure` (skip TLS verify, dev only).

## 2. Command surface

```
artifactctl collection create <name> [--public|--unlisted|--protected|--private]
                                      [--list] [--no-list]
                                      [--crawlable] [--index] [--no-index]
                                      [--expires <ts>] [--expires-in <dur>]
artifactctl collection list
artifactctl collection show <name>
artifactctl collection delete <name> [--force]

artifactctl put <collection>/<path> <localfile>
        [--content-type <ct>] [--header k=v ...]
        [--public|--protected|--bearer-token <t>|--generate-bearer-token|--basic <user:pass>]
        [--expires <ts>] [--expires-in <dur>] [--no-index]
artifactctl put-tree <collection> <localdir>
        [--content-type-auto] [--public] [--no-index] [--header k=v ...]
        [--include glob] [--exclude glob] [--large-upload-threshold <bytes>]
artifactctl get <collection>/<path> [-o <localfile>]
artifactctl rm [-r] <collection>/<path>

artifactctl meta get <collection>[/<path>]
artifactctl meta set <collection>[/<path>] [--visibility ...] [--expires ...]
                                           [--header k=v] [--crawlable] [--index] [--no-index]
        # bare <collection> = collection metadata; trailing / = folder
        # (materialized on set); otherwise the file at that path

artifactctl share create <collection>[/<path>]
        [--unlisted|--protected] [--bearer-token <t>|--generate-bearer-token] [--basic <user:pass>]
        [--expires-in <dur>] [--expires <ts>]
artifactctl share list <collection>
artifactctl share revoke <share-id>

artifactctl tree <collection> [--path <p>] [--depth <n>]
artifactctl crawl <collection> [--path <p>] [--depth <n>]

artifactctl token create <name> [--scope ...] [--expires-in <dur>]
artifactctl token list
artifactctl token revoke <id>

artifactctl maintenance cleanup
artifactctl version
```

## 3. Behavior notes

- `put` infers content-type from extension unless `--content-type` is given;
  `put-tree --content-type-auto` does this per file (SPEC §15, §20.1).
- `--generate-bearer-token` creates a random token and prints it once
  (SPEC §20.2/§20.3). `--bearer-token` always takes an explicit value; the CLI
  deliberately has no optional-value flags, so every flag parses with the same
  space-separated grammar.
- `put-tree` walks the directory, applies include/exclude globs, uploads each
  file under `<collection>/<relative-path>`, using large-upload sessions for
  files over a threshold. Reports a summary.
- `--output json` makes every command emit machine-readable JSON for scripting
  and agents (the CLI is explicitly meant to be agent-friendly, SPEC §15).
- Exit codes: `0` ok, `1` generic error, `2` usage, `4` auth, `5` not-found.

## 4. Mapping to API

| CLI | API |
|-----|-----|
| `collection create` | `POST /collections` |
| `put` | `PUT /collections/{c}/files/{path}` (or begin-upload for large) |
| `put-tree` | many `PUT` / begin-upload |
| `get` | `GET /collections/{c}/files/{path}` |
| `meta get/set` | `GET`/`PATCH .../meta` |
| `share create/list/revoke` | `/collections/{c}/shares`, `/shares/{id}` |
| `tree` | `GET /collections/{c}/tree` |
| `crawl` | `GET /collections/{c}/crawl` |
| `token *` | `/tokens` |
| `maintenance cleanup` | `POST /maintenance/cleanup` |

## 5. Distribution

Same repo, second `main` under `cmd/artifactctl`. Released as static binaries
for linux/darwin (amd64/arm64). Shares `pkg/apiclient` with the MCP adapter so
request/response handling lives in exactly one place.
