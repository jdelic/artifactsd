# artifactsd — Open Questions, Round 2

Your round-1 answers (in the now-removed `OPEN-QUESTIONS.md`) are folded into the
design docs and beads. A few answers raised follow-ups. Each item has my
**recommendation** — if you're happy with it, no action needed; the design
already proceeds on the recommendation. Override by editing the **Decision**
line. Same format as before; ⭐ = most worth your attention.

---

## N1. MCP transport — pick one ⭐

You said (E4): *"Do not do one then switch to the other. Stick with the intended
system."* So I'll commit to a single MCP transport rather than stdio-now/HTTP-later.

- **stdio** — the agent runtime spawns `artifactsd mcp` as a local child process.
  Standard for desktop/local agents; no network exposure.
- **Streamable HTTP** — the MCP server is reachable over HTTP (can be part of the
  running `artifactsd`, or a sidecar). Fits remote agents and the fact that
  artifactsd is already a network server; one deployment model.
- **Recommendation:** **Streamable HTTP**, because the whole point is publishing
  artifacts to possibly-remote agents over the network, and it avoids shipping a
  separate local-process integration.
- **Decision:**

## N2. OIDC admin gating specifics ⭐

From A1/D1 I designed admin as OIDC-gated on a group claim, configured by env
(see [14 §7](design/14-access-control.md)). Please confirm the shape:

- Env: `ARTIFACTSD_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_REDIRECT_URL`,
  `ARTIFACTSD_ADMIN_GROUP_CLAIM` (default `groups`), `ARTIFACTSD_ADMIN_GROUP`
  (required value), optional basic fallback
  `ARTIFACTSD_ADMIN_BASIC_USER`/`_PASSWORD`.
- Browser admin → OIDC redirect; **API admin clients → 401 + Bearer JWT** (a
  redirect makes no sense for non-browsers).
- Single admin issuer (per-object OIDC entries may use other issuers).
- **Recommendation:** as above.
- **Decision:**

## N3. `any_authenticated` semantics

For per-object OIDC, `any_authenticated: true` means *any* validly-signed,
non-expired token from the configured `issuer` with the right `audience` is
allowed (no subject/group whitelist).
- **Recommendation:** as above (issuer + audience still enforced; only the
  subject/group whitelist is waived).
- **Decision:**

## N4. CLI binary name after the rename

You made `artifactsd` canonical (A2). The CLI in the SPEC is `artifactctl`.
- Options: keep **`artifactctl`** (reads well, SPEC-aligned) vs **`artifactsctl`**
  (matches the `artifactsd` spelling, but clumsier).
- **Recommendation:** keep **`artifactctl`**.
- **Decision:**

## N5. Config file — confirming "none" ⭐

You said config should be **flags + envvars (12factor)**. I removed the YAML
config file entirely; the server reads only flags and the process environment
([12](design/12-config.md)). Per-object metadata (robots blocks, custom headers)
is still set through the API/CLI, not server config.
- One consequence: there is **no file** to express server defaults; everything is
  env (e.g. `ARTIFACTSD_DEFAULT_VISIBILITY=public`).
- **Recommendation:** no config file; rely on the orchestrator/dotenv for env
  injection.
- **Decision:**

## N6. Access-log view dropped from admin UI

You said access logs go to **stdout** (F5). That means the admin UI can no longer
show a served-requests view (nothing is stored). SPEC §14 listed "view access
logs". The admin UI keeps the **audit/deletion log** (DB-backed, SPEC §11/§17);
operational access logs are consumed from stdout by your log aggregation.
- **Recommendation:** drop the in-UI access-log view; keep audit/deletion log.
- **Decision:**

## N7. Self-contained vs strict 12factor (state)

12factor prefers stateless processes with external backing stores, but you want
**self-contained** with local SQLite + local blobs (B3). These conflict slightly:
the process owns local disk state (DB + blobs), so it is not horizontally
scalable or ephemeral. I'm treating the local data dir as the single declared
backing store (portable per B1) and applying 12factor to **config and logs**
only.
- **Recommendation:** self-containment wins; 12factor applies to config/logs,
  not to externalizing storage.
- **Decision:**

---

If all recommendations are fine, just say so and I'll lock them in; otherwise
edit the Decision lines and I'll fold them in.
