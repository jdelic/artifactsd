# 16 — Per-collection grants for API tokens

> **Status: ACCEPTED** (owner decisions 2026-11-14, artifactsd-m9e.26).
> Implemented: store, auth and API in artifactsd-m9e.28; admin UI and CLI in
> artifactsd-m9e.29.

## 1. Problem

API tokens are global. A `collections:read` token can download every file in
every collection through `/api/v1/collections/{c}/files/…` — including private
and auth-protected ones, because the management API bypasses object policy by
design ([04](04-http-api.md)). A `collections:write` token can modify every
collection, and `shares:write` can mint a share for anything. There is no way to
hand an agent a token limited to "its" collection, so one leaked or
prompt-injected agent token exposes the whole server.

Delete is already separated from write (`collections:delete` / `shares:delete`,
artifactsd-m9e.25), and the MCP adapter forwards each agent's own token
(artifactsd-m9e.18) — so per-agent tokens exist; what is missing is limiting
*which collections* a token reaches and *what* it may do in each.

## 2. Model: grants in their own table

A token is either **unrestricted** (today's behaviour: its token-level scopes
apply to every collection) or **restricted**: it can only reach collections it
holds a **grant** for, and each grant carries its own scopes.

```sql
-- migration NNNN
ALTER TABLE api_tokens ADD COLUMN restricted BOOLEAN NOT NULL DEFAULT 0;

CREATE TABLE token_grants (
    token_id      INTEGER NOT NULL REFERENCES api_tokens(id)  ON DELETE CASCADE,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    scopes        TEXT    NOT NULL,      -- JSON array, e.g. ["collections:read","shares:write"]
    created_at    TEXT    NOT NULL,
    PRIMARY KEY (token_id, collection_id)
);
CREATE INDEX idx_token_grants_collection ON token_grants(collection_id);
```

- Grants reference collections **by ID**, not by name: deleting a collection
  removes its grants (cascade), and a new collection that reuses the name is
  *not* covered by an old grant.
- **Restriction is an explicit flag** (decision D3b). A restricted token with no
  grants reaches nothing; deleting its last granted collection never widens it.
  Existing tokens stay unrestricted (`restricted = 0`).
- **Scopes live on the grant** (decision D2a). Allowed grant scopes:
  `collections:read|write|delete` and `shares:read|write|delete`, with the usual
  implications (`:write` ⇒ `:read`, `:delete` ⇒ `:read`, `:write` ⇏ `:delete`).
  A restricted token's token-level `scopes` column is empty; only grants count.
- **Admin tokens cannot be restricted** (D4): creating a token that is both
  `admin` and restricted is a `400`. `admin` is never a grant scope. An admin
  token can mint unrestricted tokens anyway, so a restriction would be cosmetic.

## 3. Creating collections

**Restricted tokens cannot create collections** (decision D2b): `POST
/api/v1/collections` with a restricted token is `403`. Collections are created by
an admin (or an unrestricted token), and an admin grants them to agent tokens.
Agents that need a collection per run are given one by the operator or by an
orchestrator holding an unrestricted token.

## 4. Enforcement

`auth.Principal` gains `Restricted bool` and `Grants map[collectionID]ScopeSet`
(loaded with the token; restricted tokens are a small set, one query per
authenticated request is fine at artifactsd's scale).

Two steps, so no route can forget the check:

1. **Route gate** (`requireScope`): an unrestricted principal must satisfy the
   route's scope as today. A restricted principal passes the gate if *any* of its
   grants satisfies it; the required scope is put in the request context.
   A route whose scope no grant holds is therefore `403` before any collection
   is looked up — independent of the collection, so it discloses nothing.
2. **Collection gate** (`lookupCollection` and every place that resolves a
   collection indirectly): a restricted principal needs a grant for that
   collection satisfying the route's scope.
   - **No grant for the collection ⇒ `404 not_found`** ("collection does not
     exist"), identical to a missing collection (decision D3): a restricted
     agent learns nothing about collections outside its grants.
   - Grant exists but lacks the scope ⇒ `403 forbidden` "insufficient scope:
     requires collections:write on <collection>" (the token can already see
     that collection, so nothing is disclosed).

Routes that resolve a collection without `{c}`:

| Route | Rule for restricted tokens |
|-------|----------------------------|
| `GET /collections` | only collections with a grant satisfying `collections:read` |
| `POST /collections` | `403` (§3) |
| `GET /crawl` | only granted collections (as above) |
| `GET/PATCH/DELETE /shares/{share_id}` | load the share, then the collection gate on its collection (`404` if no grant) |
| `PUT/GET/DELETE /uploads/{upload_id}` | capability routes, no token — unchanged; the session was created through `/collections/{c}/uploads`, which is gated |
| `/upload-requests`, `/tokens`, `/audit`, `/stats`, `/maintenance` | admin-only; restricted tokens are never admin (§2) |

## 5. Managing grants (admin-only API)

```
POST   /api/v1/tokens                         {"name":…, "restricted": true,
                                               "grants": [{"collection":"reports","scopes":["collections:read","collections:write"]}]}
GET    /api/v1/tokens                         each token shows restricted + grants (collection name + scopes)
PUT    /api/v1/tokens/{id}/grants/{c}         {"scopes":[…]}   create or replace one grant
DELETE /api/v1/tokens/{id}/grants/{c}         remove one grant
```

Unknown collections, empty scope lists, `admin` in a grant, or grants on an
unrestricted token are `400`.

## 6. Audit

Grant changes (token created with grants, grant set/removed) are audited as
`token.grant` / `token.ungrant` with the collection and scopes. Requests a
restricted token makes outside its grants are answered `404` and logged at
**debug** level only, not audited (decision D5) — otherwise a leaked token could
flood the audit table.

## 7. Surfaces

- **Admin UI:** the token form gets a "restricted" checkbox (scopes left
  unchecked; `admin` refused); each restricted token's row lists its grants
  with a Remove button and a grant form (collection picker + grant-scope
  checkboxes, which replace an existing grant's scopes). A collection's Shares
  screen lists the restricted tokens holding grants on it.
- **CLI:** `artifactctl token create <name> --restricted --grant reports=collections:read,collections:write`
  (repeatable), `artifactctl token grant <id> <collection> --scope …`,
  `artifactctl token ungrant <id> <collection>`, `token list` shows grants.
- **MCP:** no adapter change — it forwards each client's own token
  (artifactsd-m9e.18); a restricted agent simply sees fewer collections.

## 8. Migration and compatibility

Additive: `restricted` defaults to 0, so every existing token keeps global
access; the grants table starts empty.

artifactsd is in **alpha: downgrades are not supported** (decision D6). Note for
operators, stated once in the release notes: an older binary ignores
`restricted` and `token_grants`, so restricted tokens would become global after
a downgrade — revoke them first if you ever roll back.

## 9. Decisions (2026-11-14)

| # | Question | Decision |
|---|----------|----------|
| D1 | Model | Separate `token_grants` table (token → collection by ID), explicit `restricted` flag |
| D2a | Where permissions live | Per grant (scopes on each grant row) |
| D2b | Restricted tokens creating collections | Not allowed (`403`) |
| D3 | Out-of-grant response | `404` (non-disclosure); granted-but-insufficient scope `403` |
| D3b | Meaning of "no grants" | Explicit flag; restricted + no grants = reaches nothing |
| D4 | Admin tokens | Cannot be restricted; `400` at creation |
| D5 | Audit out-of-grant requests | No; debug log only. Grant changes are audited |
| D6 | Downgrade | Not supported in alpha; release note only |

## 10. Implementation tickets

1. **Store + auth + API** — migration, `Principal.Restricted/Grants`, the two
   gates of §4 on every route, grant management API (§5), audit (§6). Tests:
   - restricted token with every grant scope on A: every route on A succeeds;
     every route on B → 404; `GET /collections` and `GET /crawl` list only
     A; writes on A → 403 naming the scope;
   - grant with `read+write` on A, `read` on B: write on B → 403;
   - `POST /collections` with a restricted token → 403;
   - share routes by `share_id` for a collection without a grant → 404;
   - collection deleted → grants gone; re-created with the same name → 404;
   - restricted + no grants → reaches nothing (403 at the route gate);
     `admin` + restricted → 400;
   - existing (unrestricted) tokens behave exactly as before.
2. **Admin UI + CLI + docs** — §7 surfaces; docs/http-api.md, docs/cli.md,
   design/04 §1/§8, design/14 §6.
