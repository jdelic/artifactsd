# 09 — Admin web UI

Implements SPEC §14. Served at `/admin`, always behind authentication.

## 1. Approach

- **Server-rendered** `html/template` pages + **htmx** for partial updates and
  forms. No SPA, no Node/JS build toolchain — keeps the single-binary promise.
  Templates and a small static bundle (htmx, minimal CSS) are embedded via
  `embed.FS`.
- All mutations go through the same `/api/v1` handlers (or shared service layer)
  the API uses — the admin UI is a thin presentation layer, not a parallel
  implementation.
- Progressive enhancement: core flows work without JS where feasible; htmx
  improves UX.

Rationale for htmx over a JS framework: the UI is CRUD over collections/files/
metadata — well served by server rendering; avoids a build step and large
dependency surface ("don't over-engineer", AGENT_INSTRUCTIONS §8). Flagged in
OPEN-QUESTIONS in case a richer SPA is desired.

## 2. Authentication & session

- Admin auth per config (`admin.auth`, default basic; SPEC §19). On success,
  issue a secure session cookie (HttpOnly, SameSite=Lax, Secure under TLS).
- All `/admin` routes require a valid session; `/admin/login` and `/admin/logout`
  manage it. CSRF tokens on every state-changing form.
- Admin access always requires authentication (SPEC §14); no anonymous admin.

## 3. Screens (SPEC §14 checklist)

| Screen | Function |
|--------|----------|
| Dashboard | counts, storage usage, recent deletions, janitor status, "Run cleanup" button |
| Collections | list/create/delete collections; visibility & crawl badges |
| Collection detail / tree | browse file tree, upload, delete, drill into folders |
| File detail | view/download, edit metadata (content-type, headers, access, crawl, expiry) |
| Metadata editor | structured form per section + raw YAML/JSON view |
| Shares | list/create/revoke share URLs; copy link; set expiry & policy |
| Access policy | set visibility, directory listing, auth credentials (bearer/basic/oidc) |
| Crawl/index policy | toggle crawlable/indexable, robots block editor |
| Expiry | view/edit expiry rules; show effective absolute expiry |
| Tokens | list/create/revoke API tokens (secret shown once) |
| Audit log | filter by action/date; deletion + admin actions |
| Access log | recent served requests |
| OIDC settings | (M2) configure issuers/audiences |
| Maintenance | trigger cleanup, view janitor stats |

## 4. Upload UX

- Small files: direct form `PUT` to the API.
- Large files: the UI calls the begin-upload endpoint and streams via the
  returned `upload_url` (fetch + progress), per [07](07-storage-uploads.md).
- Folder/tree upload mirrors `artifactctl put-tree` (multiple files under a
  prefix) — optional for first cut.

## 5. Security specifics

- CSRF protection, same-origin checks on mutations.
- Never render secrets; show only hash-presence and token prefixes.
- Token secret and newly created share URLs shown once with a copy button.
- All admin actions audited ([06 §7](06-auth-security.md)).

## 6. Scope staging

MVP admin UI: login, dashboard, collections list/create/delete, tree browse,
upload/delete file, metadata edit (incl. visibility/expiry/crawl), shares,
tokens, audit log, run cleanup. OIDC settings and access-log analytics can
follow in M2.
