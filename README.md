# artifactsd

**A self-hosted artifact publishing server for agents and humans, with
per-object access policy, expiry, browsing, and crawl controls.**

`artifactsd` is a single Go binary that stores, manages, and publishes
artifacts created by AI agents, automation systems, CI jobs, and humans —
HTML reports, PDFs, images, audio, video, JSON, logs, archives, or any other
file. Uploaded artifacts are served over HTTP with correct content types,
configurable access control, optional directory browsing, automatic
expiry-based cleanup, and crawler/indexing policy.

It needs no external web server, no external database, and no cloud object
storage: blobs live on the local filesystem and metadata in an embedded SQLite
database. Run it, point a DNS record at it, and publish.

```console
$ artifactsd serve --listen :443 \
    --base-url https://artifacts.example.com \
    --data ./artifactsd-data
```

## Features

- **Public and protected HTTP serving** — human-readable
  `/c/{collection}/{path}` URLs and hard-to-guess `/s/{share_id}/...` share
  URLs, each with its own policy and expiry.
- **Per-object metadata** — content type and headers, access policy, crawl/
  index policy, and expiry, set per collection, folder, or file, inheriting
  downward.
- **Four visibility modes** — `public`, `unlisted`, `protected`, `private`.
- **Multiple auth mechanisms** — bearer tokens, Basic Auth, and OIDC (which
  also gates the admin UI).
- **Automatic expiry** — `expires_at`, `delete_after_creation`, and
  `delete_after_last_access`; a background janitor reaps expired objects.
- **Crawler controls** — generated `robots.txt`, `X-Robots-Tag`, `sitemap.xml`,
  and an authenticated agent crawl API that beats scraping HTML listings.
- **Admin web UI** — browse trees, edit metadata, manage shares, tokens, and
  the audit log.
- **`artifactctl` CLI** — an agent- and script-friendly client over the HTTP
  API.
- **MCP adapter** — ten agent-friendly tools over Streamable HTTP.
- **TLS built in** — serve TLS directly from files or via automatic Let's
  Encrypt (ACME), or run behind a reverse proxy.
- **Deploys itself** — `artifactsd systemd` generates (or installs) a systemd
  unit that runs the server or MCP adapter as the bare binary or under
  docker/podman, system-wide or per-user; release Docker images and example
  Nomad/Kubernetes specs are also provided.

## Documentation

- [Installation](docs/installation.md) — build, install, and run.
- [Quickstart](docs/quickstart.md) — first collection, upload, and share in a
  few commands.
- [Configuration reference](docs/configuration.md) — every flag and
  `ARTIFACTSD_*` environment variable.
- [HTTP API reference](docs/http-api.md) — the canonical `/api/v1` management
  interface.
- [CLI reference](docs/cli.md) — every `artifactctl` command.
- [MCP adapter](docs/mcp.md) — the agent tool surface.
- [`SKILL.md`](SKILL.md) — a drop-in skill teaching an AI agent how to publish
  artifacts with the CLI or MCP tools.

The original requirements spec lives in
[`.agent-docs/SPEC.md`](.agent-docs/SPEC.md) and the detailed design in
[`.agent-docs/design/`](.agent-docs/design/README.md).

## How it fits together

```
artifactsd
├── Public HTTP file server      (/c/..., /s/...)
├── HTTP management API           (/api/v1/...)
├── Admin web interface           (/admin)
├── MCP adapter                   (artifactsd mcp)
├── SQLite metadata database
├── Local blob storage
└── Expiry janitor
```

The HTTP API is the canonical interface. `artifactctl` and the MCP adapter are
both thin clients over it (sharing `pkg/apiclient`), so request/response
handling lives in exactly one place.

## Status and scale

`artifactsd` is a self-contained single process intended for an audience of up
to roughly 50 viewers per artifact. For mass distribution, put a CDN in front
or publish to S3 — that is explicitly out of scope here.

## License

BSD 3-Clause © 2026 Jonas Maurus. See [LICENSE](LICENSE).
