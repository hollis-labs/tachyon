import { beforeEach, describe, expect, it, vi } from 'vitest'

const hostFetch = vi.fn()
vi.mock('@tachyon/host-sdk', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))
import { createTesseractReadClient } from './client'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
beforeEach(() => hostFetch.mockReset())

describe('findByKey', () => {
  it('recalls the namespace, matches the key exactly and reads the newest hit', async () => {
    hostFetch.mockResolvedValueOnce(json({ results: [
      { item_id: 'OLD', key: 'k', updated_at: '2026-01-01T00:00:00Z' },
      { item_id: 'NEW', key: 'k', updated_at: '2026-02-01T00:00:00Z' },
      { item_id: 'OTHER', key: 'k2', updated_at: '2027-01-01T00:00:00Z' },
    ] }))
    hostFetch.mockResolvedValueOnce(json({ item_id: 'NEW', key: 'k' }))
    const item = await createTesseractReadClient().findByKey('knowledge@project/a/b#k')
    expect(item?.item_id).toBe('NEW')
    const [recall, get] = hostFetch.mock.calls.map((c) => String(c[0]))
    expect(recall).toContain('/recall?')
    expect(recall).toContain('namespace=project%2Fa%2Fb')
    expect(recall).toContain('domains=knowledge')
    expect(get.endsWith('/items/NEW')).toBe(true)
  })

  it('is null when no key matches, and a 404 item is null rather than an error', async () => {
    hostFetch.mockResolvedValueOnce(json({ results: [{ item_id: 'X', key: 'other' }] }))
    expect(await createTesseractReadClient().findByKey('project/a#k')).toBeNull()
    hostFetch.mockResolvedValueOnce(json({ code: 'not_found' }, 404))
    expect(await createTesseractReadClient().getItem('MISSING')).toBeNull()
  })

  it('throws Tesseract’s message on other failures', async () => {
    hostFetch.mockResolvedValueOnce(json({ code: 'auth_required', message: 'missing or invalid bearer token' }, 401))
    await expect(createTesseractReadClient().getItem('X')).rejects.toThrow('missing or invalid bearer token')
  })
})
