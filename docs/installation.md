# Installation

`artifactsd` ships as two static Go binaries:

- **`artifactsd`** — the server (and the `mcp` adapter subcommand).
- **`artifactctl`** — the command-line client.

## Download a release

Each tagged release publishes static binaries for `linux`/`darwin` ×
`amd64`/`arm64` as `.tar.gz` archives on the
[GitHub releases page](https://github.com/jdelic/artifactsd/releases), built
with [GoReleaser](https://goreleaser.com) (see [`.goreleaser.yml`](../.goreleaser.yml)).
Each archive also bundles `LICENSE` and `README.md`; for a
[systemd unit](#systemd-service), the binary generates one itself. Verify
against `checksums.txt`:

```console
$ curl -LO https://github.com/jdelic/artifactsd/releases/download/vX.Y.Z/artifactsd_X.Y.Z_linux_amd64.tar.gz
$ curl -LO https://github.com/jdelic/artifactsd/releases/download/vX.Y.Z/checksums.txt
$ sha256sum --check --ignore-missing checksums.txt
$ tar xzf artifactsd_X.Y.Z_linux_amd64.tar.gz
```

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
targets. `make release-snapshot` runs GoReleaser locally (`make release-tools`
installs it) to produce the same archives as a real release, without
publishing anything.

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

## Docker

Each release publishes a multi-arch (`linux/amd64`, `linux/arm64`) server
image to `registry.maurus.net/agent-tools/artifactsd:latest`, built from the repository's
[`Dockerfile`](../Dockerfile) (a minimal, non-root, distroless-based image
containing only the `artifactsd` binary):

```console
$ docker run -d \
    --name artifactsd \
    -p 8080:8080 \
    -v artifactsd-data:/data \
    -e ARTIFACTSD_BASE_URL=https://artifacts.example.com \
    registry.maurus.net/agent-tools/artifactsd:latest
```

The image's entrypoint is `artifactsd`; its default command is
`serve --listen :8080 --data /data`. Pass additional flags after the image
name, or override settings with `ARTIFACTSD_*` environment variables (see
[Configuration](configuration.md)) — `--base-url` (or
`ARTIFACTSD_BASE_URL`) is the only one you must set. Build the image locally
with `make docker-build` or `docker build .`.

## systemd service

The binary generates its own systemd units:

```
artifactsd systemd [--server|--mcp] [--user] [--runtime local|docker|podman] [--install]
```

This produces a unit that runs the HTTP server (default) or the
[MCP adapter](mcp.md) (`--mcp`) under the selected runtime:

- `--runtime local` (default) — runs the generating binary directly, with the
  same hardening as the packaged example unit. Data lives in a
  systemd-managed `StateDirectory` (`/var/lib/artifactsd`, or
  `~/.local/state/artifactsd` for `--user`).
- `--runtime docker` / `--runtime podman` — runs the release image (override
  with `--image`) via `docker/podman run --rm`, with the `artifactsd-data`
  volume mounted at `/data` for the server.

Without `--install` the unit is printed to stdout so you can inspect or
redirect it. With `--install` it is written to `/etc/systemd/system/` (or
`~/.config/systemd/user/` with `--user`), a commented env-file skeleton is
created if none exists — `/etc/artifactsd.env` / `/etc/artifactsd-mcp.env`
system-wide, `$XDG_CONFIG_HOME/artifactsd/*.env` per-user — and the
`systemctl` commands to enable the service are printed. Re-running
`--install` refreshes the unit but never touches an existing env file.

```console
$ sudo artifactsd systemd --install                 # server, local binary, system-wide
$ artifactsd systemd --mcp --user --runtime podman --install
$ artifactsd systemd --runtime docker > artifactsd.service   # just print it
```

System-wide `--runtime local` expects the dedicated `artifactsd` system user
(the printed next steps include the `useradd` line). Rootless `podman` pairs
naturally with `--user`; `--runtime docker --user` requires your user to be
able to talk to the Docker daemon.

For a hand-maintained starting point,
[`packaging/systemd/artifactsd.service`](../packaging/systemd/artifactsd.service)
remains as an example unit, with
[`packaging/systemd/artifactsd.env.example`](../packaging/systemd/artifactsd.env.example)
and install steps in its header comment.

## Nomad

[`examples/nomad/artifactsd.nomad`](../examples/nomad/artifactsd.nomad)
is an example job spec that runs the Docker image under Nomad with a host
volume for persistent data. It includes Traefik tags for automatic routing and
a Consul health check against `/healthz`.

**Before submitting the job**, configure a `host_volume "artifactsd-data"`
block on your Nomad client(s) pointing at a directory owned by uid/gid 65532
(the distroless `nonroot` image's runtime user), then set `ARTIFACTSD_BASE_URL`
and replace `ARTIFACTSD_BOOTSTRAP_TOKEN` with a real random value.

> **count must stay at 1.** artifactsd uses in-process SQLite; multiple
> allocations would race on the same database file. For mass distribution,
> front with a CDN or object-storage proxy.

## Kubernetes / K3S

[`examples/k8s/artifactsd.yaml`](../examples/k8s/artifactsd.yaml) is a
single-file manifest containing a Secret, PersistentVolumeClaim, Deployment,
and ClusterIP Service. It works on any cluster with a default StorageClass
(K3S ships `local-path`; managed clouds use their own default).

```console
# Generate a real bootstrap token first:
kubectl create secret generic artifactsd-secrets \
  --from-literal=bootstrap-token=$(openssl rand -hex 32) \
  --dry-run=client -o yaml | kubectl apply -f -

# Then deploy:
kubectl apply -f examples/k8s/artifactsd.yaml
kubectl rollout status deployment/artifactsd
```

Set `ARTIFACTSD_BASE_URL` in the Deployment env before applying, then wire up
an Ingress controller of your choice to the `artifactsd` Service on port 80.

> **replicas must stay at 1** and the Deployment strategy is set to `Recreate`
> (not `RollingUpdate`) to ensure no two pods ever write to the database
> simultaneously. For mass distribution, front with a CDN or object-storage
> proxy.

## Next steps

- [Quickstart](quickstart.md) — publish your first artifact.
- [Configuration](configuration.md) — admin auth, OIDC, limits, the janitor.
