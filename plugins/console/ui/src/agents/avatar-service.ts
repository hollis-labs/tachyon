import { operatorFetch } from '../operator/elevation'
import palette from './avatar-palette.json'

/** Read-only image surface of the Station avatar service. It needs no token; CORS only matters for fetch, not <img>. */
export const AVATAR_BASE = (import.meta.env?.VITE_AVATAR_BASE_URL as string | undefined) ?? 'https://avatars.nanite.cloud/img'
/** Tachyon backend routes that hold the service token and call avatar_generate / avatar_delete. */
const SAVE_BASE = '/api/plugins/console/v1/avatars/'

/** The service's shared palette table (data, not theme); a slot name is what avatar_generate accepts. */
export const PALETTE: readonly { slot: string; accent: string; bg: string }[] = palette

/** Colours are the seed's own, a palette slot, or an explicit accent and background pair. */
export type AvatarColors = { kind: 'seed' } | { kind: 'slot'; slot: string } | { kind: 'custom'; accent: string; bg: string }
export interface AvatarKnobs { seed: string; fill: number; colors: AvatarColors }
export const DEFAULT_FILL = 65

/** The service stores avatars under a sanitised, NFC-normalised name. */
export const avatarName = (urn: string) => urn.normalize('NFC').replace(/[^\p{L}\p{N}_.@+-]/gu, '_')

const hex = (value: string) => value.replace('#', '').toLowerCase()

/** A pure function of the knobs: the same URL always draws the same image and nothing is stored. */
export function previewUrl(knobs: AvatarKnobs, format: 'svg' | 'png' = 'svg', size?: number): string {
  const q = new URLSearchParams({ seed: knobs.seed, fill: String(knobs.fill), format })
  if (knobs.colors.kind === 'slot') q.set('palette', knobs.colors.slot)
  if (knobs.colors.kind === 'custom') { q.set('accent', hex(knobs.colors.accent)); q.set('bg', hex(knobs.colors.bg)) }
  if (size) q.set('size', String(size))
  return `${AVATAR_BASE}/preview?${q}`
}

export function storedUrl(urn: string, ext: 'svg' | 'png' | '@8x.png' = 'svg', version = 0): string {
  return `${AVATAR_BASE}/avatars/${encodeURIComponent(avatarName(urn))}${ext === '@8x.png' ? '@8x.png' : '.' + ext}${version ? `?v=${version}` : ''}`
}

// A saved or cleared avatar bumps this so every <img> of it refetches.
let revision = 0
const listeners = new Set<() => void>()
export const avatarRevision = { get: () => revision, subscribe: (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn) } } }
const bump = () => { revision += 1; listeners.forEach(fn => fn()) }

export type AvatarResult = { ok: true } | { ok: false; unavailable?: boolean; message: string }
export interface AvatarClient {
  save(urn: string, knobs: AvatarKnobs): Promise<AvatarResult>
  clear(urn: string): Promise<AvatarResult>
}

export function createAvatarClient(fetcher: typeof fetch = operatorFetch): AvatarClient {
  async function send(urn: string, method: 'PUT' | 'DELETE', body?: unknown): Promise<AvatarResult> {
    try {
      const res = await fetcher(SAVE_BASE + encodeURIComponent(urn), { method, headers: { Accept: 'application/json', ...(body ? { 'Content-Type': 'application/json' } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
      // A server that has no such route may answer 200 with its HTML shell; only a JSON reply counts as saved.
      if (res.ok && (res.headers.get('content-type') ?? '').includes('json')) { bump(); return { ok: true } }
      if (res.ok) return { ok: false, unavailable: true, message: 'Saving avatars is not connected to the Tachyon backend yet.' }
      if ([404, 405, 501].includes(res.status)) return { ok: false, unavailable: true, message: 'Saving avatars is not connected to the Tachyon backend yet.' }
      const data = await res.json().catch(() => ({})) as { error?: string; message?: string }
      return { ok: false, message: data.message ?? data.error ?? `The avatar service refused the request (${res.status}).` }
    } catch (error) {
      return { ok: false, message: error instanceof Error ? error.message : 'The avatar request failed.' }
    }
  }
  return {
    save: (urn, knobs) => send(urn, 'PUT', {
      mode: 'svg', force: true, fill: knobs.fill,
      ...(knobs.colors.kind === 'slot' ? { palette: knobs.colors.slot } : {}),
      ...(knobs.colors.kind === 'custom' ? { accent: knobs.colors.accent, bg: knobs.colors.bg } : {}),
    }),
    clear: urn => send(urn, 'DELETE'),
  }
}
export const avatarClient = createAvatarClient()
