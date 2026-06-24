# 13 — Testing & quality

Per AGENT_INSTRUCTIONS: state-of-the-art linters/build checks first, tests
before/with implementation, never skip quality gates.

## 1. Toolchain & gates

| Gate | Tool | Notes |
|------|------|-------|
| Build | `go build ./...` | cross-compile linux/darwin amd64/arm64 (CGO permitted, decision B2; default pure-Go for build simplicity) |
| Vet | `go vet ./...` | baseline |
| Lint | `golangci-lint` | curated: govet, staticcheck, errcheck, gosec, revive, gocritic, ineffassign, misspell, bodyclose, sqlclosecheck |
| Format | `gofumpt` (+ `goimports`) | enforced in CI |
| Tests | `go test ./... -race` | race detector on |
| Coverage | `go test -cover` | track; aim meaningful coverage on policy/meta/auth |
| Vuln scan | `govulncheck ./...` | dependency CVEs |
| Tidy | `go mod tidy -diff` | no drift |

A `Makefile`/`Taskfile` target `make check` runs the full set; CI runs the same.

## 2. Test strategy

- **Unit tests** for the high-risk pure logic:
  - `meta` resolution/merge (inheritance precedence, header merge, auth replace).
  - `policy` decisions (visibility × auth × expiry → 401/403/404/serve, ancestor
    guard, non-disclosure).
  - `expiry` computation (earliest-in-tree always wins, container cascade,
    duration parsing).
  - path validation / traversal rejection (table-driven, adversarial inputs).
  - argon2id + token hashing round-trips; constant-time behavior.
- **Store tests** against a real temp SQLite DB (migrations applied): CRUD,
  cascades, denormalized-column recompute, refcount blob GC.
- **HTTP handler tests** with `httptest`: API contracts, status codes, auth
  middleware, rate limiting, header filtering, range/conditional serving.
- **Serving integration**: end-to-end `/c` and `/s` with policy combinations;
  robots.txt and sitemap content; crawl API filtering.
- **Janitor tests**: time-injected clock; expiry deletion + audit; upload/blob
  GC; `delete_after_last_access` throttle.
- **CLI tests**: command parsing + golden output; against an in-process test
  server.
- **MCP tests**: tool schema + mapping to API via a fake client.
- **Spawned-process smoke test** (`make smoke` / `scripts/smoke-test.sh`):
  builds the real `artifactsd`/`artifactctl` binaries, spawns `artifactsd
  serve` as a separate OS process over a real TCP listener (with
  `ARTIFACTSD_BOOTSTRAP_TOKEN`, [12 §2.1](12-config.md)), waits for
  `/healthz`, then drives it through `artifactctl` (collection, put/get, meta,
  tree, share, token, maintenance) plus `curl` for the couple of things no CLI
  command exposes (raw HTTP status codes, and a SHA256 check of a file served
  over `/c`). Distinct from the in-process `testserver` e2e tests above, which
  never leave the test binary's own process.

Use an injectable `clock` interface (no real sleeps) for expiry/throttle/rate
tests. Use `t.TempDir()` for data dirs.

## 3. Fixtures & helpers

- A `testserver` helper that spins up the full server on an ephemeral port with
  a temp data dir for integration tests (also reused by CLI/MCP tests).
- Golden files for robots.txt, sitemap.xml, directory listings, crawl JSON.

## 4. Security testing

- `gosec` in lint set.
- Explicit adversarial tests: path traversal, unsafe headers, oversized uploads,
  auth brute-force → 429, private/expired → 404 (no existence leak), share id
  unguessability (entropy check), CSRF on admin forms.
- `/security-review` style pass before milestone completion.

## 5. CI

- GitHub Actions (repo is on GitHub: `jdelic/artifactsd`): matrix build + `make
  check` + `govulncheck`. Cache modules. Block merge on red.
- Beads workflow (AGENT_INSTRUCTIONS): file follow-up issues, run gates locally
  before push.

## 6. Docs as deliverable

- Keep `.agent-docs/design/*` updated when design changes during implementation
  (AGENT_INSTRUCTIONS building-phase rule 3).
- User-facing `README.md` + `docs/` (install, quickstart, config reference, API
  reference, CLI reference) produced alongside the relevant epics.
