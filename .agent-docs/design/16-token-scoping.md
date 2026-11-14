# 16 — Per-collection scoping for API tokens

> **Status: PROPOSAL — awaiting owner sign-off** (artifactsd-m9e.26, raised in
> the 2026-11-13 security review). Nothing here is implemented. Once the
> decisions in §8 are confirmed, the implementation tickets in §9 are filed.

## 1. Problem

API tokens are global. A `collections:read` token can download every file in
every collection through `/api/v1/collections/{c}/files/…` — including private
and auth-protected ones, because the management API bypasses object policy by
design ([04](04-http-api.md)). A `collections:write` token can modify every
collection, and `shares:write` can mint a share for anything. There is no way to
hand an agent a token limited to "its" collection, so one leaked or
prompt-injected agent token exposes the whole server.

Delete is already separated from write (`collections:delete` / `shares:delete`,
artifactsd-m9e.25), and the MCP adapter now forwards each agent's own token
(artifactsd-m9e.18) — so per-agent tokens exist; what is missing is limiting
*which collections* a token reaches.

## 2. Restriction model

**Recommendation: (c) — one optional list holding both exact names and
`prefix*` patterns.**

```sql
-- migration NNNN (additive)
ALTER TABLE api_tokens ADD COLUMN collections TEXT; -- JSON array, NULL = all
```

- `NULL` (or column absent in old rows) ⇒ **unrestricted** — today's behaviour.
- Otherwise a JSON array of entries, each either an exact collection name
  (`"reports-2026"`) or a prefix pattern ending in a single `*`
  (`"agent-foo-*"`). No other glob syntax (no `?`, `[]`, `**`), no regex.
- Validation at token creation: every entry is a valid collection name
  ([02](02-data-model.md) name rules), optionally with one trailing `*`; a bare
  `*` is rejected (that is "unrestricted" — omit the field instead); at most 64
  entries; the array must not be empty (an empty list would be a token that can
  reach nothing — reject as a likely mistake).
- Matching: exact entries compare equal; `p*` matches any name with prefix `p`.
  Case-sensitive, like collection names.

Why both: exact names cover "this agent owns `report-x`"; prefixes cover agents
that create collections on the fly (`agent-foo-<run-id>`), which is the common
CI/agent pattern. Prefix-only would force operators to invent prefixes for
single collections; exact-only can't express "collections this agent creates".

## 3. Creating collections

**Recommendation:** a restricted token may `POST /collections` **only if the new
name matches one of its entries** (exact or prefix); otherwise `403
forbidden` "token is not allowed to create collection X". This is what makes
prefix patterns useful for agents, and it cannot be used to reach existing
collections (creation fails with 409 on an existing name *after* the scope
check, so the response must not reveal existence for out-of-scope names — check
scope first, then existence).

## 4. Enforcement point

One helper, used everywhere a collection is resolved for a request:

```go
// collectionAllowed reports whether the request's principal may touch name.
func collectionAllowed(p *auth.Principal, name string) bool
```

- `Handler.lookupCollection` (all `/collections/{c}/…` routes) calls it after
  the scope check and before the store lookup.
- **Out of scope ⇒ `404 not_found` ("collection does not exist")**, identical to
  a missing collection. Recommendation: 404, not 403 — the whole point is that a
  restricted agent learns nothing about collections outside its scope, and 404
  is how the public routes already treat non-disclosure (design/03 §5).
- Routes that resolve a collection indirectly:

| Route | Rule |
|-------|------|
| `GET /collections` | filter the list to allowed names |
| `POST /collections` | §3 |
| `GET /crawl` | filter to allowed collections |
| `GET/PATCH/DELETE /shares/{share_id}` | load the share, check its collection; out of scope ⇒ 404 |
| `PUT/GET/DELETE /uploads/{upload_id}` | capability routes, no token involved — unchanged (the session was created through `/collections/{c}/uploads`, which is checked) |
| `/upload-requests`, `/tokens`, `/audit`, `/stats`, `/maintenance` | admin-only, and admin tokens are never restricted (§5) — unchanged |

## 5. Interaction with scopes

- Restrictions narrow **where** a token acts; scopes still decide **what** it may
  do (`collections:read|write|delete`, `shares:read|write|delete`). Both must
  allow a request.
- **`admin` tokens cannot be restricted**: reject `admin` + `collections` at
  creation (400). An admin token can mint an unrestricted token anyway, so a
  restriction would be cosmetic.
- The bootstrap token is admin ⇒ unrestricted.
- Shares minted by a restricted `shares:write` token are bound to an allowed
  collection by construction (§4). The share URL itself (`/s/…`) is a
  capability and is not token-scoped.

## 6. Surfaces

- **API:** `POST /api/v1/tokens` accepts `"collections": ["reports-2026",
  "agent-foo-*"]`; `GET /api/v1/tokens` returns it (null = all).
- **Admin UI:** token form gains a "Limit to collections" text field
  (comma/newline separated), disabled when `admin` is ticked; the token list
  shows the restriction.
- **CLI:** `artifactctl token create <name> --scope … --collection reports-2026
  --collection 'agent-foo-*'` (repeatable); `token list` shows it.
- **MCP:** no adapter change — it forwards client tokens (artifactsd-m9e.18).
- **Audit:** token creation records the restriction in the audit details; a
  request rejected for scope reasons is *not* audited (it is a 404 like any
  missing collection — avoids an audit-flood vector).

## 7. Migration

Additive column, default `NULL` ⇒ all existing tokens stay unrestricted. No data
migration, no breaking change. Older binaries ignore the column (they select an
explicit column list), so a rollback is safe.

## 8. Decisions for the owner

| # | Question | Recommendation |
|---|----------|----------------|
| D1 | Model | Exact names + trailing-`*` prefixes in one list (§2) |
| D2 | Restricted token creating collections | Allowed only for matching names (§3) |
| D3 | Out-of-scope response | 404 (non-disclosure), not 403 (§4) |
| D4 | Admin tokens | Cannot be restricted; reject at creation (§5) |
| D5 | Audit of out-of-scope requests | Not audited (§6) |

## 9. Implementation tickets (to file after sign-off)

1. **Store + API:** migration, `APIToken.Collections`, `auth.Principal`
   carries the list, `collectionAllowed`, enforcement per §4, token create/list
   API. Tests:
   - restricted `collections:read` token: allowed collection 200; other
     collection 404 on every `{c}` route (files, meta, tree, folders, crawl,
     shares, uploads begin); `GET /collections` and `GET /crawl` filtered;
   - `POST /collections` matching prefix → 201, non-matching → 403, existing
     out-of-scope name → 403 (not 409);
   - share routes by `share_id` for an out-of-scope collection → 404;
   - `admin` + `collections` → 400; unrestricted tokens behave as before;
   - migration leaves existing tokens unrestricted.
2. **Admin UI + CLI + docs:** form field, `--collection` flag, `token list`
   output, docs/http-api.md, docs/cli.md, design/04 §1/§8, design/14 §6.
