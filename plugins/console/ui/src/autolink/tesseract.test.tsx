/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { createDefaultRefRegistry, TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND } from '../refs'
import type { TesseractItem, TesseractReadClient } from '../tesseract/client'
import { AutolinkProvider, Linkified } from '.'
import { createLiveResolvers } from './live'
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
