/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { createDefaultRefRegistry, TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND } from '../refs'
import type { TesseractItem, TesseractReadClient } from '../tesseract/client'
import { AutolinkProvider, Linkified } from '.'
import { createLiveResolvers } from './live'
import { setRefLabelMax, truncateLabel } from './labelPrefs'
import { createTesseractCache, TesseractContext } from './tesseractDetail'

afterEach(cleanup)

const ID = '01M4SCJHDM8FV6MDPSDTMZ0X44'
const KEY = 'project/portfolio-architect/workspace/state#current_state'
const item: TesseractItem = {
  item_id: ID, domain: 'workspace', namespace: 'project/portfolio-architect/workspace/state', key: 'current_state',
  tags: ['state', 'pilot'], payload: { summary: 'Live state', body: '## Goals\n\n- **CW-20261016-0101** first' },
  updated_at: '2026-10-19T00:45:27Z', created_at: '2026-10-13T01:03:33Z', author: { agent_id: 'msg://agent/x/y' },
}

function fakeClient(over: Partial<TesseractReadClient> = {}): TesseractReadClient {
  return {
    getItem: vi.fn(async (id: string) => (id === ID ? item : null)),
    findByKey: vi.fn(async (c: string) => (c === KEY ? item : null)),
    ...over,
  }
}

function setup(client: TesseractReadClient, ui: React.ReactNode) {
  const cache = createTesseractCache(client)
  const api = { plannerTaskTitles: async () => ({}), plannerResolve: async () => { throw new Error("unused") } }
  return render(
    <MemoryRouter>
      <TesseractContext.Provider value={cache}>
        <AutolinkProvider registry={createDefaultRefRegistry()} resolvers={createLiveResolvers(api, cache)}>{ui}</AutolinkProvider>
      </TesseractContext.Provider>
    </MemoryRouter>,
  )
}

describe('Tesseract references', () => {
  it('resolves an item id and a namespace#key to the same item, and an unknown id to not-found', async () => {
    const client = fakeClient()
    setup(client, <Linkified text={`${ID} and ${KEY} and 01M4SCJHDM8FV6MDPSDTMZ0X99`} />)
    await waitFor(() => expect(document.querySelectorAll('[data-ref-state="found"]')).toHaveLength(2))
    expect(document.querySelectorAll('[data-ref-state="not-found"]')).toHaveLength(1)
    expect([...document.querySelectorAll('[data-ref-kind]')].map((c) => c.getAttribute('data-ref-kind'))).toEqual([TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND, TESSERACT_ITEM_KIND])
  })

  it('opens the detail drawer with the markdown body, whose ids link too', async () => {
    setup(fakeClient(), <Linkified text={ID} />)
    await waitFor(() => expect(document.querySelector('[data-ref-state="found"]')).not.toBeNull())
    fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement)
    const detail = await screen.findByTestId('tesseract-detail')
    expect(detail.textContent).toContain('Live state')
    expect(detail.textContent).toContain('current_state')
    expect(detail.querySelector('[data-ref-kind="torque.task"]')).not.toBeNull()
    expect(detail.textContent).toContain('pilot')
  })

  it('shows a retryable error when Tesseract cannot be read', async () => {
    const getItem = vi.fn().mockRejectedValueOnce(new Error('boom')).mockResolvedValue(item)
    setup(fakeClient({ getItem }), <Linkified text={ID} />)
    await waitFor(() => expect(document.querySelector('[data-ref-state="error"]')).not.toBeNull())
  })
})

describe('createTesseractCache', () => {
  it('serves one request to every caller and honours the TTL', async () => {
    const client = fakeClient()
    let t = 0
    const cache = createTesseractCache(client, { ttlMs: 1000, now: () => t })
    await Promise.all([cache.ensure(TESSERACT_ITEM_KIND, ID), cache.ensure(TESSERACT_ITEM_KIND, ID)])
    expect(client.getItem).toHaveBeenCalledTimes(1)
    await cache.ensure(TESSERACT_ITEM_KIND, ID)
    expect(client.getItem).toHaveBeenCalledTimes(1)
    t = 2000
    await cache.ensure(TESSERACT_ITEM_KIND, ID)
    expect(client.getItem).toHaveBeenCalledTimes(2)
  })
})

describe('resolved chip labels', () => {
  it('reads "Type: Title" once resolved, cut to the configured length, and keeps the id until then', async () => {
    const long: TesseractItem = { ...item, tags: ['handoff'], payload: { ...item.payload, title: 'A very long handoff title that goes on and on past any sensible chip width' } }
    setup(fakeClient({ getItem: vi.fn(async () => long) }), <Linkified text={ID} />)
    const chip = () => document.querySelector('[data-ref-kind]') as HTMLElement
    expect(chip().textContent).toBe('01M4…0X44')
    await waitFor(() => expect(chip().getAttribute('data-ref-state')).toBe('found'))
    expect(chip().textContent).toBe('Handoff: A very long handoff title that goes on…')
    expect(chip().textContent!.length).toBeLessThanOrEqual(48)
    setRefLabelMax(24)
    await waitFor(() => expect(chip().textContent!.length).toBeLessThanOrEqual(24))
    expect(chip().textContent!.endsWith('…')).toBe(true)
    setRefLabelMax(null)
  })

  it('labels tasks "Task: Title"', async () => {
    const api = { plannerTaskTitles: async () => ({ 'CW-20261016-0101': { id: 'CW-20261016-0101', title: 'Reference registry', status: 'done', priority: 1, updated_at: '2026-10-16T05:00:00Z' } }), plannerResolve: async () => { throw new Error('not used') } }
    render(
      <MemoryRouter>
        <AutolinkProvider registry={createDefaultRefRegistry()} resolvers={createLiveResolvers(api as never)}><Linkified text="CW-20261016-0101" /></AutolinkProvider>
      </MemoryRouter>,
    )
    await waitFor(() => expect(document.querySelector('[data-ref-state="found"]')?.textContent).toBe('Task: Reference registry'))
  })
})

describe('truncateLabel', () => {
  it('collapses whitespace and ends with one ellipsis', () => {
    expect(truncateLabel('a  b\nc', 10)).toBe('a b c')
    expect(truncateLabel('abcdefghij', 5)).toBe('abcd…')
    expect(truncateLabel('abc', 5)).toBe('abc')
  })
})
