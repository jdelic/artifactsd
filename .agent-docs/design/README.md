# artifactd — Design Documentation

This directory contains the detailed design for `artifactd`, the self-contained
artifact publishing server specified in [`../SPEC.md`](../SPEC.md).

The design is split into focused documents. Read them in order for a full
picture, or jump to the area you are working on.

| # | Document | Scope |
|---|----------|-------|
| 01 | [Architecture](01-architecture.md) | Component map, process model, technology choices |
| 02 | [Data model](02-data-model.md) | SQLite schema, object model, IDs, migrations |
| 03 | [Metadata & policy](03-metadata-policy.md) | Metadata document, inheritance/resolution, visibility |
| 04 | [HTTP management API](04-http-api.md) | `/api/v1` surface, request/response contracts |
| 05 | [Serving, crawl & robots](05-serving-crawl.md) | Public serving pipeline, robots.txt, sitemap, crawl API |
| 06 | [Auth & security](06-auth-security.md) | Bearer/basic/OIDC, hashing, path safety, rate limits |
| 07 | [Storage & uploads](07-storage-uploads.md) | Blob layout, direct & large uploads, integrity |
| 08 | [Expiry janitor](08-expiry-janitor.md) | Expiry model, janitor loop, deletion logging |
| 09 | [Admin web UI](09-admin-ui.md) | Operator interface, rendering approach |
| 10 | [CLI (`artifactctl`)](10-cli.md) | Command surface, config, output |
| 11 | [MCP adapter](11-mcp.md) | Agent-facing tools over the HTTP API |
| 12 | [Configuration](12-config.md) | Config file, env, flags, defaults |
| 13 | [Testing & quality](13-testing-quality.md) | Test strategy, linters, CI gates |

See also:
- [`../ROADMAP.md`](../ROADMAP.md) — milestones and how the beads epics map to this design.
- [`../OPEN-QUESTIONS.md`](../OPEN-QUESTIONS.md) — decisions that need operator (Jonas) input.

## Decision summary (defaults chosen during planning)

These are the working assumptions baked into the design. Each one that is a
genuine judgement call is also listed in `OPEN-QUESTIONS.md` so it can be
confirmed or overridden before implementation.

| Area | Decision | Rationale |
|------|----------|-----------|
| Language / runtime | Go 1.23+, single static binary | Spec requirement; easy ops |
| SQLite driver | `modernc.org/sqlite` (pure Go, CGO-free) | Static cross-compiled binary, no libc/cgo |
| HTTP routing | stdlib `net/http` + Go 1.22 `ServeMux` patterns | Minimal deps, sufficient routing |
| Config format | YAML (`gopkg.in/yaml.v3`) | Matches SPEC examples |
| CLI framework | `spf13/cobra` | Multi-level subcommands in SPEC §15 |
| Password hashing | argon2id (`golang.org/x/crypto/argon2`) | SPEC §17, matches `$argon2id$` example |
| Token hashing | random 256-bit token, stored SHA-256 | High-entropy tokens → fast hash + index OK |
| Admin UI | server-rendered `html/template` + htmx, no JS build | Single binary, no node toolchain |
| Asset/migration bundling | `embed.FS` | Single self-contained binary |
| Metadata storage | JSON document column + denormalized index columns | Flexible merge + queryable expiry/visibility |
| Logging | stdlib `log/slog`, JSON or text | Structured, no dep |
| TLS | direct cert/key files; optional `autocert` later | SPEC §17 "support TLS directly" |
| MCP SDK | official `github.com/modelcontextprotocol/go-sdk` | First-party, maintained — pending confirmation |
| OIDC | deferred to post-MVP (M2) | SPEC §21 |

## Naming

- Server daemon binary: `artifactd`
- CLI binary: `artifactctl`
- Go module path: `github.com/jdelic/artifactsd`
- Repository: `artifactsd` (note the trailing `s`); the product/binary is `artifactd`.
