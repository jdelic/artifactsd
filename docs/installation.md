# Installation

`artifactsd` ships as two static Go binaries:

- **`artifactsd`** — the server (and the `mcp` adapter subcommand).
- **`artifactctl`** — the command-line client.

## Build from source

You need Go (see [`go.mod`](../go.mod) for the required version).

```console
$ git clone https://github.com/jdelic/artifactsd
$ cd artifactsd
$ go build ./cmd/artifactsd
$ go build ./cmd/artifactctl
```

Or install them onto your `GOPATH`/`GOBIN`:

```console
$ go install github.com/jdelic/artifactsd/cmd/artifactsd@latest
$ go install github.com/jdelic/artifactsd/cmd/artifactctl@latest
```

The repository's [`Makefile`](../Makefile) wraps the common build, test, and lint
targets.

## Running the server

The server is configured entirely with command-line flags and `ARTIFACTSD_*`
environment variables — there is no config file (see
[Configuration](configuration.md)). The only required setting is the public
base URL:

```console
$ artifactsd serve \
    --listen :443 \
    --base-url https://artifacts.example.com \
    --data ./artifactsd-data
```

On first boot the server creates the data directory, applies database
migrations (unless `--auto-migrate=false`), and starts serving. The data
directory holds everything:

```
artifactsd-data/
├── artifactsd.db        # SQLite metadata
├── blobs/               # artifact bytes
└── acme/                # ACME cert cache (when --tls=acme)
```

### Subcommands

| Command | Purpose |
|---------|---------|
| `artifactsd serve` | Run the server. |
| `artifactsd migrate` | Apply database migrations and exit. |
| `artifactsd hash-password` | Produce an argon2id hash for the admin basic-auth fallback. |
| `artifactsd mcp` | Run the MCP adapter (see [MCP](mcp.md)). |
| `artifactsd version` | Print version/build information. |

## TLS

`artifactsd` can terminate TLS itself or sit behind a reverse proxy:

- `--tls=off` (default) — plain HTTP; terminate TLS at a proxy in front of it
  and set `--trust-proxy` so forwarded headers are honored.
- `--tls=files --tls-cert=cert.pem --tls-key=key.pem` — serve a supplied
  certificate.
- `--tls=acme --acme-domains=artifacts.example.com --acme-email=you@example.com`
  — automatic Let's Encrypt certificates, cached under `<data>/acme`.

See [Configuration → TLS](configuration.md#tls) for the full matrix.

## Next steps

- [Quickstart](quickstart.md) — publish your first artifact.
- [Configuration](configuration.md) — admin auth, OIDC, limits, the janitor.
