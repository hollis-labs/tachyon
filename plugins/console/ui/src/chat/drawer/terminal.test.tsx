import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const calls: { url: string; method: string; body?: string }[] = []
vi.mock('../../operator/elevation', () => ({
  operatorFetch: async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body as string | undefined })
    const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status })
    if (url.endsWith('/machines')) return json({ machines: [{ machine_id: 'dev-box', label: 'dev-box' }] })
    if (url.includes('/proposals?')) return json({ proposals: [{ id: 'p1', command: 'ls', requested_by: 'Agent', state: 'pending', op_digest: 'd1' }] })
    if (url.endsWith('/proposals/p1/edit')) return json({ id: 'p2', command: 'ls -la', requested_by: 'Agent', state: 'pending', op_digest: 'd2' }, 201)
    if (url.endsWith('/proposals/p2/run')) return json({ run_id: 'r' })
    return json({ message: 'nope' }, 404)
  },
}))
vi.mock('./PtyTerminal', () => ({ PtyTerminal: () => null }))
import { TerminalPane } from './TerminalPane'

afterEach(() => { cleanup(); calls.length = 0 })

describe('terminal pane', () => {
  it('edit creates a new proposal that Run then approves with its own digest', async () => {
    render(<TerminalPane threadRef="tether:s1" />)
    await screen.findByText('ls')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.change(screen.getByLabelText('Edit command'), { target: { value: 'ls -la' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('ls -la')
    fireEvent.click(screen.getByRole('button', { name: /Run/ }))
    await waitFor(() => expect(calls.some((c) => c.url.endsWith('/proposals/p2/run') && JSON.parse(c.body ?? '{}').op_digest === 'd2')).toBe(true))
    expect(calls.every((c) => c.url.startsWith('/api/plugins/console/v1/terminal/'))).toBe(true)
  })
})
