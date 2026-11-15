/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { createDefaultRefRegistry } from '../refs'
import { createMockResolver } from '../refs/test-resolver'
import { AutolinkProvider, Linkified } from '.'

afterEach(cleanup)

describe('kinds whose read is not free', () => {
  it('planner and Tether chips wait for hover instead of resolving on mount', async () => {
    const onCall = vi.fn()
    const resolver = createMockResolver({ 'DEC-052': { title: 'Done means merged' }, cp_928c2c714c131ae1c23ff24b: { title: 'Plan' } }, { onCall })
    render(
      <MemoryRouter>
        <AutolinkProvider registry={createDefaultRefRegistry()} resolvers={{ 'planner.decision': resolver, 'tether.plan': resolver }}>
          <Linkified text="DEC-052 and cp_928c2c714c131ae1c23ff24b" />
        </AutolinkProvider>
      </MemoryRouter>,
    )
    const chips = document.querySelectorAll('[data-ref-kind]')
    expect(chips.length).toBe(2)
    await new Promise((r) => setTimeout(r, 30))
    expect(onCall).not.toHaveBeenCalled()
    fireEvent.pointerEnter(chips[0])
    await waitFor(() => expect(onCall).toHaveBeenCalledTimes(1))
    expect(onCall.mock.calls[0][0]).toEqual(['DEC-052'])
  })
})
