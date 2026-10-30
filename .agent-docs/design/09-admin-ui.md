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
- The retro Norton-Commander footer advertises keyboard shortcuts as
  **Alt-`<letter>`** combos (e.g. `Alt-C` Create, `Alt-L` Logout), wired by a
  small `static/shortcuts.js` keydown handler to the element carrying a matching
  `data-shortcut` attribute. DOS-style function keys (F5/F8/F10) are deliberately
  avoided: browsers reserve them (reload, menu, …) and a page cannot intercept
  them reliably (artifactsd-9sy). Shortcuts cover the single, unambiguous action
  per screen (create/upload/filter/save/cleanup) plus logout; per-row actions
  (delete/revoke) stay as on-row buttons since a global key cannot pick a row.
  When a letter is bound both in the page content and in the top nav (`Alt-U`:
  Upload on the tree screen, Upload Requests elsewhere), the page's binding
  wins, matching what its footer advertises.

Rationale for htmx over a JS framework: the UI is CRUD over collections/files/
metadata — well served by server rendering; avoids a build step and large
dependency surface ("don't over-engineer", AGENT_INSTRUCTIONS §8). Confirmed by
decision E1 ("simple beats complex here").

## 2. Authentication & session

- **Admin is OIDC-gated** (decisions A1/D1): login runs the OIDC browser flow and
  access is granted only when the token carries the required group claim
  (`ARTIFACTSD_ADMIN_GROUP` in `ARTIFACTSD_ADMIN_GROUP_CLAIM`). An optional
  basic/htaccess fallback (env-configured) is also supported. Config is entirely
  env-based ([12 §2](12-config.md), [14 §7](14-access-control.md)).
- On success, issue a secure session cookie (HttpOnly, SameSite=Lax, Secure
  under TLS). `/auth/oidc/login`, `/auth/oidc/callback`, `/admin/logout` manage
  the session. CSRF tokens on every state-changing form.
- Admin access always requires authentication (SPEC §14); no anonymous admin.

## 3. Screens (SPEC §14 checklist)

| Screen | Function |
|--------|----------|
| Dashboard | counts, storage usage, recent deletions, janitor status, "Run cleanup" button |
| Collections | list/create/delete collections; visibility & crawl badges; one-click **Share** (whole collection) |
| Files (all) | cross-collection file browser showing size/created/**expiry**, with sorting (largest-first default, **expiring-next**, by-name, newest), linking to each object and its metadata editor |
| Shares (all) | cross-collection overview of every share; revoke in place |
| Collection detail / tree | browse file tree, upload (with an optional destination subfolder that creates nested folders), delete, drill into folders; one-click **Share** on any file/folder (and the current folder) that mints a /s/ link inline |
| File detail | view/download, edit metadata (content-type, headers, access, crawl, expiry) |
| Metadata editor | structured form per section (incl. visibility, directory listing, crawl/index, **sandbox** tri-state, expiry with per-mechanism enable toggles + effective-expiry banner) with per-field info tooltips, plus the full **JSON policy document** editor; save errors render inline next to the section that produced them |
| Shares | list/create/revoke share URLs; copy link; set expiry & policy |
| Access policy | set visibility, directory listing, auth credentials (bearer/basic/oidc) |
| Crawl/index policy | toggle crawlable/indexable, robots block editor |
| Expiry | view/edit expiry rules; show effective absolute expiry |
| Tokens | list/create/revoke API tokens (secret shown once) |
| Audit log | filter by action/date; deletions, admin actions, **and authenticated access to protected objects** (`object.access`, decision N6); DB-backed |
| Maintenance | trigger cleanup, view janitor stats |

(No operational access-log screen: public/served-request logs stream to stdout,
decision F5 — only restricted-content access is in the DB audit log. OIDC/admin
auth is env-configured, not edited in the UI.)

## 4. Upload UX

- The tree page has a single upload form for files of any size: it calls the
  begin-upload endpoint and streams via the returned `upload_url` (XHR PUT with
  a progress bar), per [07](07-storage-uploads.md). The admin UI has no direct
  small-file upload of its own; the API's direct `PUT .../files/{path}` for
  small files is unaffected.
  A failed begin or PUT shows `error (<status>): <message>`, taking the message
  from the admin's or the API's JSON error body, or the first line of a
  plain-text one (the CSRF gate); a proxy's HTML error page shows only the
  status.
- Folder/tree upload mirrors `artifactctl put-tree` (multiple files under a
  prefix) — optional for first cut.

## 5. Security specifics

- CSRF protection, same-origin checks on mutations.
- Never render secrets; show only hash-presence and token prefixes.
- Token secret and newly created share URLs shown once with a copy button.
- All admin actions audited ([06 §7](06-auth-security.md)).

## 6. Scope staging

MVP admin UI: OIDC login, dashboard, collections list/create/delete, tree
browse, upload/delete file, metadata edit (incl. visibility/expiry/crawl/auth),
shares, tokens, audit/deletion log, run cleanup.
