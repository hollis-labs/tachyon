import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const calls: { url: string; method: string; body?: string }[] = []
vi.mock('../../operator/elevation', () => ({
  operatorFetch: async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET', body: init?.body as string | undefined })
    const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status })
    if (url.endsWith('/machines')) return json({ machines: [{ machine_id: 'example-box', label: 'example-box' }] })
    if (url.includes('/proposals?')) return json({ proposals: [{ id: 'p1', command: 'ls', requested_by: 'Agent', state: 'pending', command_digest: 'd1' }] })
    if (url.endsWith('/proposals/p1/edit')) return json({ id: 'p2', command: 'ls -la', requested_by: 'Agent', state: 'pending', command_digest: 'd2' }, 201)
    if (url.endsWith('/proposals/p1/run') || url.endsWith('/proposals/p2/run')) {
      const digest = url.endsWith('/p1/run') ? 'd1' : 'd2'
      if (JSON.stringify(JSON.parse(init?.body as string)) !== JSON.stringify({ op_digest: digest })) return json({ message: 'The reviewed digest must match.' }, 409)
      return json({ run_id: 'r' })
    }
    return json({ message: 'nope' }, 404)
  },
}))
vi.mock('./PtyTerminal', () => ({ PtyTerminal: () => null }))
import { TerminalPane } from './TerminalPane'

afterEach(() => { cleanup(); calls.length = 0 })

describe('terminal pane', () => {
  it('Run maps the listed proposal command_digest to the reviewed op_digest', async () => {
    render(<TerminalPane threadRef="tether:s1" />)
    await screen.findByText('ls')
    fireEvent.click(screen.getByRole('button', { name: /Run/ }))
    await waitFor(() => expect(screen.queryByText('ls')).toBeNull())
    const run = calls.find((c) => c.url.endsWith('/proposals/p1/run'))
    expect(run?.method).toBe('POST')
    expect(JSON.parse(run?.body ?? '{}')).toEqual({ op_digest: 'd1' })
  })
  it('edit creates a new proposal that Run then approves with its own digest', async () => {
    render(<TerminalPane threadRef="tether:s1" />)
    await screen.findByText('ls')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.change(screen.getByLabelText('Edit command'), { target: { value: 'ls -la' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('ls -la')
    fireEvent.click(screen.getByRole('button', { name: /Run/ }))
    await waitFor(() => expect(screen.queryByText('ls -la')).toBeNull())
    const run = calls.find((c) => c.url.endsWith('/proposals/p2/run'))
    expect(run?.method).toBe('POST')
    expect(JSON.parse(run?.body ?? '{}')).toEqual({ op_digest: 'd2' })
    expect(calls.some((c) => c.url.endsWith('/proposals/p1/run'))).toBe(false)
    expect(calls.every((c) => c.url.startsWith('/api/plugins/console/v1/terminal/'))).toBe(true)
  })
})
