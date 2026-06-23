# artifactsd — Design Documentation

This directory contains the detailed design for `artifactsd`, the self-contained
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
| 12 | [Configuration](12-config.md) | Flags + env (12factor), defaults |
| 13 | [Testing & quality](13-testing-quality.md) | Test strategy, linters, CI gates |
| 14 | [Access control](14-access-control.md) | **Consolidated** auth model: bearer/basic/OIDC, admin gating, capabilities |

See also:
- [`../ROADMAP.md`](../ROADMAP.md) — milestones and how the beads epics map to this design.
- [`../OPEN-QUESTIONS-2.md`](../OPEN-QUESTIONS-2.md) — round-2 questions raised by the round-1 answers.

## Decision summary (after operator round-1 answers)

The round-1 QA answers are folded in below. Items still needing confirmation are
in `OPEN-QUESTIONS-2.md`.

| Area | Decision | Source |
|------|----------|--------|
| Language / runtime | Go 1.23+, single binary | SPEC |
| Name | `artifactsd` is canonical (daemon, db, env prefix, data dir); CLI `artifactctl` | A2 |
| License | BSD 3-clause, © Jonas Maurus | A4 |
| SQLite driver | `modernc.org/sqlite` default (pure Go); **CGO permitted** | B2 |
| Database | SQLite only, single-node, self-contained | B3 |
| HTTP routing | stdlib `net/http` + Go 1.22 `ServeMux` | — |
| **Configuration** | **flags + env only (12factor); no config file** | F4/G |
| **Logging** | **access logs → stdout; app/errors → stderr; audit → DB + stderr** | F5 |
| CLI framework | `spf13/cobra` + charmbracelet (lipgloss/bubbletea/huh) for UX | E2 |
| Password hashing | argon2id | D3 |
| Token hashing | random ≥256-bit, stored SHA-256 | D3 |
| Admin UI | server-rendered `html/template` + htmx, no JS build | E1 |
| **Admin auth** | **OIDC group-gated (+ basic fallback), env-configured** | A1/D1 |
| **OIDC** | **in MVP**; whitelist by subject/email, group claim, or any-authenticated | A1 |
| Default visibility | **`public`** (no auth unless configured); listing/crawl/index off | C5 |
| `access.auth` inheritance | replace; multiple entries any-of | C1/C2 |
| Protected disclosure | bearer→404, basic→401, oidc→redirect/401 | C4 |
| Expiry | earliest-in-tree always wins; **no override**; container cascade | C3 |
| Upload URLs | unguessable capability, **no extra auth**, short TTL | D2 |
| **TLS** | direct cert/key **or ACME/autocert from first release** | D4 |
| Blob layout | content-addressed behind `blob.Store` (portable data dir) | B1 |
| Asset/migration bundling | `embed.FS` | — |
| MCP SDK / transport | SDK no preference; **single committed transport** (N1, recommend Streamable HTTP) | E3/E4 |
| Resumable uploads | M2 milestone | F2 |

## Naming

- Server daemon binary: `artifactsd`
- CLI binary: `artifactctl`
- Go module path: `github.com/jdelic/artifactsd`
- Env prefix: `ARTIFACTSD_`; DB `artifactsd.db`; data dir `artifactsd-data`;
  upload metadata header `Artifactsd-Metadata`.
