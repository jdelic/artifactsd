# artifactd — Roadmap & Milestones

Milestones group the beads epics. Each epic is tracked in beads (label
`milestone:m0|m1|m2`, plus `area:*`). See [`design/`](design/) for the detailed
design behind each epic.

## Milestone M0 — Foundations

Make the project buildable, testable, and able to store bytes + metadata.

| Epic | Area | Design |
|------|------|--------|
| Project scaffolding & tooling | build, config, logging | [01](design/01-architecture.md), [12](design/12-config.md), [13](design/13-testing-quality.md) |
| Storage & data model | store, db, blob | [02](design/02-data-model.md), [07](design/07-storage-uploads.md) |

Exit: `make check` green in CI; migrations apply; blob store + collection/node
CRUD with tests.

## Milestone M1 — MVP (SPEC §21)

Everything in the SPEC MVP list: HTTP serving + management API, auth (bearer/
basic), visibility modes, directory listing toggle, expiry janitor, robots/
X-Robots-Tag/sitemap, crawl API, admin UI, CLI, minimal MCP, docs.

| Epic | Area | Design |
|------|------|--------|
| Metadata & policy engine | meta, policy | [03](design/03-metadata-policy.md) |
| Auth & security | auth, security | [06](design/06-auth-security.md) |
| HTTP server & middleware | server | [01](design/01-architecture.md) |
| Management API | api | [04](design/04-http-api.md) |
| Uploads & blob integration | uploads | [07](design/07-storage-uploads.md) |
| Public serving | serve | [05](design/05-serving-crawl.md) |
| Crawl, robots & sitemap | crawl | [05](design/05-serving-crawl.md) |
| Expiry janitor | janitor | [08](design/08-expiry-janitor.md) |
| Admin web UI | admin | [09](design/09-admin-ui.md) |
| CLI (`artifactctl`) | cli | [10](design/10-cli.md) |
| MCP adapter | mcp | [11](design/11-mcp.md) |
| Docs & release | docs | [13](design/13-testing-quality.md) |

Exit: a single binary can be launched per SPEC §1 quickstart and satisfy all of
SPEC §20 use cases; full test suite + security tests green.

## Milestone M2 — Post-MVP (SPEC §21)

| Epic | Area | Notes |
|------|------|-------|
| OIDC & post-MVP hardening | auth, security | OIDC verifier + admin OIDC, ACME/autocert, resumable uploads, blob scrub, richer admin analytics |

Exit: OIDC-protected objects and admin; automated TLS; resilience features.

## Dependency overview (epic level)

```
scaffolding ─┬─▶ storage ─┬─▶ metadata/policy ─┬─▶ serving ─┬─▶ crawl/robots/sitemap
             │            │                    │            └─▶ docs/release
             │            ├─▶ management API ──┼─▶ uploads ──┘
             ├─▶ auth/security ────────────────┤
             └─▶ http server/middleware ───────┘
                 management API ─▶ CLI, MCP, admin UI
                 auth + serving ─▶ OIDC/post-MVP (M2)
```

Critical path to a usable server: scaffolding → storage → metadata/policy +
auth + http server → management API → uploads → public serving.
