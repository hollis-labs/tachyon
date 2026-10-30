# File viewer contract (V0)

Final shapes for the read-only file viewer backend: `internal/fileviewapi/openapi.yaml`, fixtures in
`internal/fileviewapi/fixtures/` (sample file bodies in `fixtures/content/`), design `tesseract:01M659AR9BC7CV1YTQQTPKVTCE`
(project/tachyon/workspace/design, key `design_tachyon_file_viewer_backend_20261030`). Run the stub with
`TACHYON_FILEVIEW_STUBS=1`. There is no stream: the viewer polls the ETag.

- Prefix `/api/plugins/console/v1/files`. **Every route needs an operator session, reads included**
  (`RequireOperatorReads`), except `GET /files/frames/{id}`: a sandboxed iframe has an opaque origin and no
  cookie, so the unguessable single-use id is the capability.
- Files are on the Tachyon **host**; the browser machine's disk is never read. Read-only: nothing writes a file.
- Errors are `{code,message}`; no error echoes a path. `X-Stub-Status: 400|403|404|409|413|429|503` forces an
  error from any stub route.
- **Hidden, not forbidden.** A path outside every root, or unknown, is the same item: `exists:false,
  allowed:false, deny_reason:"not_found_or_hidden"`, so the API cannot probe the disk. Inside a root, a secret
  path is `allowed:false, deny_reason:"secret_pattern"`; others are `symlink_escape` and `too_large`.
- **Stat** is a batch (`POST /stat`, 1 to 100 paths; denied items are items, not errors). `kind` is one of
  html, markdown, text, json, yaml, toml, csv, code, image, binary (null for dirs and denied paths). `etag` is
  size+mtime+inode; poll it, there is no watcher. Directories are reported as `dir` and never listed.
- **Content** (`GET /content?path=` or `?caster_ref=`, exactly one) honours Range (206), If-Range and
  If-None-Match (304). Text, markdown, code and HTML are always `text/plain; charset=utf-8` (the UI renders
  them); images keep their type; SVG carries `Content-Security-Policy: sandbox; default-src 'none'` and must be
  used in an `<img>` only. `nosniff` everywhere. Caps: text 2 MiB inline (the rest by Range), image 25 MiB,
  HTML render 2 MiB; over a cap answers 413 `too_large`.
- **Frames render HTML.** `POST /frame {path | caster_ref}` returns `{frame_url, expires_at, etag, scripts}`;
  load `frame_url` in an iframe with `sandbox=""` (no `allow-same-origin`). The document is single use, lives
  5 minutes, and is served with `default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:;
  sandbox; frame-ancestors 'self'`, nosniff, no-store and no-referrer. Relative assets do not load: pages must
  be self-contained. `scripts:true` is refused (403 `scripts_not_allowed`) unless the app setting allows it.
  Non-HTML is 400 `not_html`; HTML over 2 MiB is 413 (show the source with a banner). A used or expired frame
  is 410 `gone`.
- **caster://** refs: `GET /resolve?ref=caster://ns/name[@rev]` gives title, tags, `rev`, `latest_rev`,
  `floating` (no `@rev` resolves to the latest and says which), `sha256` (also the content ETag) and
  `revisions[]`. Only allowed namespaces open (default designer, scratch): others are 403
  `namespace_not_allowed`.
- **Settings**: `GET/PUT /settings` (`app`: roots, deny_extra, caps, caster_namespaces, run_scripts_allowed,
  allow_user_roots, audit_retention_days; `user`: additions-only roots, F-V4 pending). A root must be absolute,
  not `/`, not the home directory, and never inside or above a secret directory (400 `invalid_root`). Built-in
  deny patterns cannot be removed. A request cannot unlock personal roots for itself (403
  `user_roots_not_allowed`). `GET /roots` is the read-only effective list with caps.
- **Audit**: `GET /audit?limit=&since=` newest first; no content is stored; denials are always recorded.

Stub limits: nothing is stored, a frame id is fixed (`StubFrameID` serves; `StubGoneFrameID` answers 410), the
PUT validates and echoes the merged settings, and the fixture user is `/home/operator`.

**Migration number:** the design names migration 019 for `fileview_audit`, but chat already uses 019 to 023 and
agents 024 to 026. V6 must take the next free number (027 at the time of writing; check `ls
internal/localstore/migrations`). V0 adds no migration.

## V6: audit (real)

Migration `032_fileview_audit.sql` (the design said 019; 028-029 are open approvals PRs, 030 catalog and 031
console item state: take the next free number at merge). `fileview.Auditor` writes `fileview_audit` through
the serialized writer: no file content, caster refs without query or fragment, paths and ids truncated at
1024 bytes, control characters removed.

- `Record` waits for the row; use it for open, frame, caster and every denial. `RecordAsync` is the best-effort
  path for stat batches (one row per batch, `count` = paths); it never blocks, a failure bumps `Dropped()`,
  and a denial passed to it is still written synchronously. A denial needs a `deny_reason`.
- `GET /audit?limit=&since=` (operator session required): newest first by id, `ts_ms > since`, limit 1..1000
  (default 200), unknown parameters are 400. `next_since` is the newest `ts_ms` returned, to pass as `since`
  on the next poll; null when nothing came back. If more than `limit` rows are newer than `since`, the oldest
  are omitted. `count` is an additive field on `AuditEntry` (1 except stat batches).
- Retention: a pass at start and then daily deletes rows older than `fileview.audit_retention_days` (1..365,
  default 30; an invalid stored value means 30). A failed or panicking pass is skipped.
- The recorders are called by V3-V5 (stat, content, frame, caster); V6 only provides them.
