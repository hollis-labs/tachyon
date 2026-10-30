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

Migration `033_fileview_audit.sql` (the design said 019; 028-029 are approvals, 030 the catalog index, 031 console item state, 032 alerts in #227: take the next free number at merge). `fileview.Auditor` writes `fileview_audit` through
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

## V1: settings and roots (real)

`internal/fileview` serves `GET /settings`, `PUT /settings` and `GET /roots` from `app_settings` rows
`fileview.roots`, `.deny_extra`, `.caps`, `.caster_namespaces`, `.run_scripts_allowed`, `.allow_user_roots`,
`.audit_retention_days`, `.user_roots` (one JSON value per row). They are mounted when the local store is
open, one pattern each, and win over the stub's subtree. Reads need an operator session
(`RequireOperatorReads`); the PUT needs an elevated one (`RequireOperator`).

- PUT changes only the fields it names, in one transaction; body cap 16 KiB, unknown fields and trailing
  data are 400. A stored row that no longer validates is ignored for that key (default), never widened.
- A root must be an absolute, existing directory; not `/`, the home directory or any parent of it; not inside
  a secret directory (`.ssh`, `.gnupg`, `.aws`, `.kube`, `.config/gcloud`), also after symlinks; not a parent
  of one. Violations are 400 `invalid_root`. At most 32 roots in total.
- User roots are additions only and need `allow_user_roots` already true in the STORED settings: a request
  cannot unlock them for itself (403 `user_roots_not_allowed`). Turning the switch off hides them without
  deleting them.
- `GET /roots` lists the effective roots: Downloads, dev, /tmp and the agent-os workspaces (each only if it
  exists; design F-V1 asks Chrispian to confirm), then app and user roots. A root that disappeared or now
  resolves somewhere invalid is dropped. `owner_only` is true for world-writable roots: V2 serves only files
  owned by the service user from them. `path_display` is `~`-relative under the service user's home.
- `fileview.BuiltinDeny` lists the always-denied basename globs for V2; `deny_extra` only adds to it.

## Path guard (V2)

`fileview.Guard` is the only code that reads the host disk for the viewer. `Resolve(ctx, path)` returns the resolved path, its root and relative path, or a `Denial`; `Open` / `OpenResolved` open a regular file read-only. Order of checks:

1. Shape: no NUL, at most 4096 bytes, absolute after `~` expansion (service user's home) and lexical clean. Otherwise `invalid_path`.
2. `EvalSymlinks` of the full path; every later check uses the resolved path. Missing, dangling or looping is `not_found_or_hidden`.
3. The resolved path must be inside an effective root (separator-aware). A path that starts inside a root and ends outside is `symlink_escape`; one that was never inside is `not_found_or_hidden`, so the host cannot be probed.
4. Secret patterns on the resolved and the requested path: basenames from `BuiltinDeny` plus the user's `deny_extra` (case-insensitive; `id_*.pub` is allowed), any secret directory segment (`.ssh`, `.gnupg`, `.aws`, `.kube`, `.config/gcloud`) and `.git/config`. `secret_pattern`.
5. Under an owner-only root (world-writable, such as `/tmp`) the file must be owned by the service uid, else `not_found_or_hidden`.
6. `OpenResolved` opens with `O_NOFOLLOW`, then checks the descriptor is a regular file (`not_a_file` otherwise) and the same file the lstat saw, so a swap or a planted link between check and open is refused.

Denial text is the reason code only; the path is never echoed. Directories resolve (for `stat`) but cannot be opened. The tests in `guard_test.go` are the corpus: traversal, absolute paths, links in and out of a root, links to secrets, dangling and looping links, every built-in pattern, case, FIFO, directory, swap-after-check, other-user files under `/tmp`, user deny additions, vanished roots. Size caps, content types and Range belong to V3.

## Stat and content (V3)

`fileview.Files`, mounted by `register_fileview.go` (one pattern each for `POST /stat` and `GET /content`, behind `RequireOperatorReads`; they win over the stub's subtree). Both go through `Guard`.

- `POST /stat {paths:[1..100]}`: items as in the contract. A bad path shape is 400 for the whole batch. A denied item has `allowed:false` and `deny_reason`; a path never inside a root, a missing path and a dangling link are all `not_found_or_hidden` (`exists:false`). `secret_pattern` and `symlink_escape` report `exists:true`. Directories report `type:"dir"`, no kind. Files over the cap for an image or binary report `too_large`; large text stays allowed (Range).
- `etag` is a strong hash of size, mtime and inode; there is no watcher, clients poll it.
- Kind and mime come from the name and the first 512 bytes. HTML is decided by content, not extension; an image by content (SVG only when the bytes contain an `<svg` element); NUL or invalid UTF-8 is binary. `language` is set for code, json, yaml, toml.
- `GET /content?path=`: `http.ServeContent`, so `Range`, `If-Range` and `If-None-Match` (206, 304, 416) work. Text, markdown, code and HTML are always `text/plain; charset=utf-8`; images keep their type (SVG adds the sandbox CSP); binary is `application/octet-stream` as an attachment. `nosniff` and `Cache-Control: private, no-cache` on everything. Text and HTML over `fileview.caps.text_bytes` need a `Range` header (else 413); images and binaries over `image_bytes` are 413 even with Range. Errors: 400 invalid (also directories and FIFOs), 403 `denied`, 404 `not_found_or_hidden`, 501 `caster_unavailable` for `caster_ref` until V5. Error bodies never contain the path.
- Audit (V6 writer): one async `stat` row per batch, a synchronous `open` row with bytes served and etag, and every denial synchronously. The principal is a hash of the session cookie.

## Frames (V4)

`fileview.Frames`, mounted by `register_fileview.go`: `POST /frame` behind `RequireOperator`, `GET /frames/{id}` with no guard (a sandboxed iframe has an opaque origin and sends no cookie; the id is the capability, so isolation comes from the sandbox CSP and not from a second origin, F-V2).

- `POST /frame {path, scripts?}` returns 201 `{frame_url, expires_at, etag, scripts}`. The file must pass the path guard and be HTML by content (else 400 `not_html`), at most `fileview.caps.html_bytes` (else 413, show the source instead). `scripts:true` is 403 `scripts_not_allowed` unless `fileview.run_scripts_allowed` is on (F-V3: off by default, per-file opt-in). `caster_ref` is 501 until V5. At most 128 live frames (429 `too_many_frames`).
- The id is 256 random bits. A frame lives five minutes, is served once, and is bound to the file's etag at mint: the file is opened through the guard again when the frame is served, and a changed file answers 410. A used or expired id answers 410 for one more minute, then 404.
- The document is served as `text/html; charset=utf-8` with `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox; frame-ancestors 'self'` (with scripts: `script-src 'unsafe-inline'` and `sandbox allow-scripts`), `nosniff`, `no-store`, `Referrer-Policy: no-referrer`. Neither policy has `allow-same-origin`, so the document stays opaque-origin. Relative assets of the page are not loaded.
- Audit: a synchronous `frame` row at mint (with etag) and every denial.

## Caster adapter (V5)

`fileview.Caster` reads `caster://ns/name[@rev]` artifacts from the caster daemon. It is on when `TACHYON_CASTER_TOKEN_FILE` names the read-scoped device token (made once on the home node with `caster pair -scope read -label tachyon -token-file PATH`); `CASTER_URL` overrides the daemon address (default `http://127.0.0.1:8991`). With the variable unset, `caster_ref` on `/content` and `/frame` stays 501 `caster_unavailable` and `/resolve` falls through to the stub (if on).

- `GET /resolve?ref=` (`RequireOperatorReads`): `{ref, namespace, name, kind, mime, size, rev, latest_rev, floating, sha256, title, tags, revisions[]}`. A ref without `@rev` is floating and reports the rev it resolved to. `revisions` (newest first) carry `rev`, `sha256`, `title`, `at`; per-revision `size` is not known to caster and is omitted. `tags` is always `[]` (head does not return them). Unknown parameters are 400.
- `GET /content?caster_ref=`: same headers and caps as host files (text, markdown, code and HTML are `text/plain`; images keep their type; SVG gets the sandbox CSP; `nosniff`). The `ETag` is the content hash, so `If-None-Match` gives 304; Range works on text. The pinned rev is read, and the bytes must hash to what `/v1/head` said, else 409 `changed` (retry).
- `POST /frame {caster_ref}`: the rev is pinned at mint, the hash is the etag, the artifact must be HTML by content and within the HTML cap. Serving re-reads the bytes and re-checks the hash and the namespace; any mismatch is 410.
- Shapes: only `caster://ns/name` or `caster://ns/name@N` (N >= 1, no leading zero); no query, fragment, userinfo or extra segments (400 `invalid`).
- Only namespaces in `fileview.caster_namespaces` (default designer, scratch) open; others are 403 `namespace_not_allowed` before any call to the daemon, and are audited as a denial. Caster itself has no per-namespace permission, so this list is the only narrowing.
- Failures: unknown ref 404 `not_found`; a missing or empty token file, a refused token or scope, an unreachable daemon, a redirect (never followed) or an unreadable answer are all 503 `caster_unavailable`. No body carries the token, the daemon address or the daemon's own error text.
- The adapter calls only `/v1/head`, `/v1/get?raw=1` and `/v1/log`; never `caster.resolve` and never the operator key. The token file is read on each request, so a renewed token needs no restart. Fetched bytes are capped (the per-kind cap, 32 MiB at most) and are never interpreted.
- Audit (V6): `caster` rows with source `caster` for `/content` opens (sync) and `/resolve` (async), `frame` rows with source `caster` at mint, and denials (`namespace_not_allowed`, `not_found_or_hidden`, `too_large`) synchronously. Refs are stored without query or fragment.
- Live use needs Chrispian's one-time `caster pair`; the tests run against a fake daemon.
