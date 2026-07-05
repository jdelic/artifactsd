---
name: verify
description: Build and drive artifactsd/artifactctl end-to-end to verify a change at its real surface (CLI, HTTP API, or MCP), instead of only re-running tests.
---

# Verifying artifactsd changes at the surface

Build the binaries into a scratch dir (never the repo):

```bash
go build -o "$SCRATCH/artifactctl" ./cmd/artifactctl
go build -o "$SCRATCH/artifactsd" ./cmd/artifactsd
```

## CLI-only commands (no server needed)

Commands like `skill install`, `version`, and config handling can be driven
directly. Isolate them: run in a scratch project dir with `HOME` (and
`XDG_CONFIG_HOME` where relevant) pointed at scratch dirs so nothing leaks
into the real home directory:

```bash
mkdir -p "$SCRATCH/proj" "$SCRATCH/home"
cd "$SCRATCH/proj" && HOME="$SCRATCH/home" "$SCRATCH/artifactctl" <cmd> ...
```

Probes that have paid off: unknown arguments, `env -u HOME`, re-running the
same command (idempotency), `--help` text still matching behavior.

## Server-backed commands

`make smoke` boots a real server and exercises the API surface — use it as
the baseline. For targeted flows, `internal/testserver` shows how tests boot
a server; for manual driving, start `artifactsd` with a config from
`examples/` and hit it with `$SCRATCH/artifactctl --base-url ... --token ...`
or curl.

Quality gates (`make check`, `make smoke`) are CI's job — run them before
pushing, but they are not verification evidence; driving the surface is.
