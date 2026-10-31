# File viewer: security review notes (V8)

For the reviewer of the file viewer stack (V1 settings, V2 guard, V3 stat/content, V4 frames, V6 audit). V5, the
caster adapter, is built (`/resolve`, `caster_ref` content and frames) but its namespace allow-list and ref audit
are not reviewed here. Per the current
posture there are no extra guards, step-up or assurance levels in this slice; the swap point stays
`operator.RequireOperator` / `RequireOperatorReads` in `register_fileview.go`.

Try it: `scripts/fileview-curl-matrix [-b base] [-r dir]` (needs `TACHYON_COOKIE`; creates and removes a scratch
directory under a served root, prints one PASS/FAIL per case, exit status = failures).

## Trust boundaries

| Surface | Who can call | Notes |
|---|---|---|
| `GET /settings`, `/roots`, `/audit`, `POST /stat`, `GET /content` | operator session cookie | `RequireOperatorReads` |
| `PUT /settings`, `POST /frame` | elevated operator session | `RequireOperator` |
| `GET /frames/{id}` | anyone holding the id | deliberately unguarded, see below |

The browser is untrusted for path choice: it sends absolute paths, the server decides. Error bodies carry a reason
code, never the path, and a path outside every root looks the same as a missing one (`not_found_or_hidden`).

## Read path (stat, content, frame mint)

All three go through `Guard`: shape, `EvalSymlinks`, inside-a-root, secret patterns on the resolved and requested
names, owner check under world-writable roots, then `O_NOFOLLOW` open with a same-file descriptor check
(`docs/fileview-contract.md`, "Path guard (V2)"). Residual points a reviewer should weigh:

- Roots are the boundary. The default set includes `/tmp` (owner-only: files must belong to the service uid) and
  the agent-os workspaces; Chrispian was asked to confirm the list (F-V1). Anything a root contains and the
  patterns do not name is readable, including source trees with committed secrets in files named unusually.
- `BuiltinDeny` is name-based. Content is never scanned, so a secret in `notes.txt` is served.
- `deny_extra` and `user_roots` change what is served; `PUT /settings` needs an elevated session and a user root
  also needs `allow_user_roots` already stored, so one request cannot unlock itself.
- TOCTOU: the open uses `O_NOFOLLOW` and compares the descriptor with the lstat, so a swap between check and open
  is refused. A file rewritten after the open is served as of the descriptor.
- Content types: text, markdown, code and HTML are always `text/plain` with `nosniff`, so `/content` never
  renders a document in the app origin. SVG is the exception that keeps its type and adds the sandbox CSP.
- Caps: text over `text_bytes` needs a `Range` request; images and binaries over `image_bytes` are refused.

## Frames (the cross-origin question)

`GET /frames/{id}` has no guard because a sandboxed iframe has an opaque origin and sends no cookie. The control
is the id and the sandbox, not a second origin (F-V2):

- 256-bit random id, served once, five-minute life, bound to the file's etag at mint; the file is re-resolved
  through the guard at serve time, so a file that became denied or changed answers 410.
- CSP `default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox; frame-ancestors 'self'`.
  No `allow-same-origin`, so the document cannot read the app's cookies, storage or DOM. No network: `connect-src`
  falls to `default-src 'none'`, forms and navigation are sandboxed.
- Scripts are off. `scripts:true` is 403 unless `fileview.run_scripts_allowed` is on; then `allow-scripts` and
  `script-src 'unsafe-inline'` are added, still without `allow-same-origin` and still with no network.
- Caveats: an id in a URL can end up in a history or log line, but is dead after one use or five minutes. `Referrer-Policy: no-referrer`
  and `no-store` are set. Frames are in memory (max 128), so a restart drops them. A Chrome bug in sandbox
  handling would expose the app origin's storage only if `allow-same-origin` were ever added; keep it out.

## Audit

Every open, frame mint and denial is written synchronously with a hash of the session cookie as principal; stat
batches are one best-effort row. No file content, and caster refs lose query and fragment. Retention is 30 days
by default. The audit read is operator-only: `GET /audit` is mounted with `RequireOperatorReads` (it was briefly mounted with
`RequireOperator`, which lets GETs through; fixed in #526).

## Not covered or skipped (posture)

- Caster adapter (V5): built; a reviewer should still check its namespace allow-list and the ref audit.
- No step-up for opening a file, no per-file consent, no rate limit on `/stat` or `/content`, no scan of file
  content for secrets: skipped as purely hardening.
- Frames are not shared across restarts and the audit has no tamper evidence.
