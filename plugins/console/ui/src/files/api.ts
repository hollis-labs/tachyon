import { hostFetch } from '@tachyon/host-sdk'

// Read-only file viewer client (docs/fileview-contract.md). Files live on the
// Tachyon host. Every route needs an operator session, reads included; this
// client is quiet on 401/403 (no elevation prompt from a hover or a drawer).
export const FILES_BASE = '/api/plugins/console/v1/files'

export type FileKind = 'html' | 'markdown' | 'text' | 'json' | 'yaml' | 'toml' | 'csv' | 'code' | 'image' | 'binary'
export type DenyReason = 'secret_pattern' | 'symlink_escape' | 'too_large' | 'not_found_or_hidden'

export interface FileStat {
  path: string
  root_label?: string | null
  rel_path?: string | null
  exists: boolean
  type?: 'file' | 'dir' | 'other' | null
  mime?: string | null
  kind?: FileKind | null
  language?: string | null
  size?: number | null
  mtime?: number | null
  allowed: boolean
  deny_reason?: DenyReason | null
  etag?: string | null
}

export interface Frame {
  frame_url: string
  expires_at: number
  etag: string
  scripts: boolean
}

/** A failed call, reduced to what the viewer can show. */
export class FileError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

async function fail(res: Response): Promise<never> {
  let code = 'error'
  let message = `Request failed (${res.status})`
  try {
    const body = (await res.json()) as { code?: string; message?: string }
    code = body.code ?? code
    message = body.message ?? message
  } catch {
    // Non-JSON error body: keep the status text.
  }
  throw new FileError(res.status, code, message)
}

// ---- caster artifacts: caster://ns/name or caster://ns/name@N (N >= 1)
const CASTER = /^caster:\/\/([A-Za-z0-9._-]+)\/([A-Za-z0-9._-]+)(?:@([1-9]\d{0,5}))?$/

export const isCasterRef = (ref: string) => ref.startsWith('caster://')

export function parseCasterRef(ref: string): { ns: string; name: string; rev: number | null } | null {
  const m = CASTER.exec(ref)
  return m ? { ns: m[1], name: m[2], rev: m[3] ? Number(m[3]) : null } : null
}

/** The same artifact pinned to one revision. */
export const pinCasterRef = (ref: string, rev: number) => `${ref.replace(/@\d+$/, '')}@${rev}`

export interface CasterInfo {
  ref: string
  namespace: string
  name: string
  kind?: FileKind | null
  mime?: string | null
  size?: number | null
  rev: number
  latest_rev: number
  floating: boolean
  sha256?: string
  title?: string | null
  revisions: { rev: number; sha256?: string; title?: string | null; at?: number | null }[]
}

/** What the shared viewer body needs, from a resolved artifact. */
export const casterStat = (c: CasterInfo, pinned: string): FileStat => ({
  path: pinned,
  exists: true,
  allowed: true,
  type: 'file',
  kind: c.kind ?? 'binary',
  mime: c.mime,
  size: c.size,
  mtime: null,
})

export const contentUrl = (path: string) => (isCasterRef(path) ? `${FILES_BASE}/content?caster_ref=${encodeURIComponent(path)}` : `${FILES_BASE}/content?path=${encodeURIComponent(path)}`)

export const fileApi = {
  async stat(path: string): Promise<FileStat> {
    const res = await hostFetch(`${FILES_BASE}/stat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ paths: [path] }),
    })
    if (!res.ok) return fail(res)
    const body = (await res.json()) as { items: FileStat[] }
    const item = body.items[0]
    if (!item) throw new FileError(502, 'invalid_response', 'The file service returned no result.')
    return item
  },
  async resolve(ref: string): Promise<CasterInfo> {
    const res = await hostFetch(`${FILES_BASE}/resolve?ref=${encodeURIComponent(ref)}`, { headers: { Accept: 'application/json' } })
    if (!res.ok) return fail(res)
    return (await res.json()) as CasterInfo
  },
  async text(path: string): Promise<{ text: string; etag: string | null }> {
    const res = await hostFetch(contentUrl(path), { headers: { 'Cache-Control': 'no-store' } })
    if (!res.ok) return fail(res)
    return { text: await res.text(), etag: res.headers.get('ETag') }
  },
  async frame(path: string): Promise<Frame> {
    const res = await hostFetch(`${FILES_BASE}/frame`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(isCasterRef(path) ? { caster_ref: path } : { path }),
    })
    if (!res.ok) return fail(res)
    return (await res.json()) as Frame
  },
}

// ---------- pure helpers ----------

export function formatSize(bytes: number | null | undefined): string {
  if (bytes == null) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** Why a file cannot be shown, in words. Never echoes a path; hidden and missing are the same message. */
export function denyMessage(reason: DenyReason | null | undefined, exists = true): string {
  switch (reason) {
    case 'secret_pattern':
      return 'Not available to view: this looks like a secret file.'
    case 'symlink_escape':
      return 'Not available to view: the link leads outside the allowed folders.'
    case 'too_large':
      return 'This file is too large to preview.'
    case 'not_found_or_hidden':
      return 'Not found, or not in a folder Tachyon can show.'
    default:
      return exists ? 'Not available to view.' : 'Not found, or not in a folder Tachyon can show.'
  }
}

/** Failure reason for an error response; 401/403 means the operator session is missing. */
export function errorMessage(e: unknown): string {
  if (e instanceof FileError) {
    if (e.status === 401 || e.status === 403) return 'Operator access is needed to view files.'
    if (e.code === 'caster_unavailable') return 'Caster is not connected on this host.'
    if (e.code === 'namespace_not_allowed') return 'This caster namespace is not available to view.'
    if (e.code === 'changed') return 'The artifact changed while loading. Reload to try again.'
    if (e.status === 413) return 'This file is too large to preview.'
    if (e.status === 404) return 'Not found, or not in a folder Tachyon can show.'
    return e.message
  }
  return e instanceof Error ? e.message : 'Could not load the file.'
}
