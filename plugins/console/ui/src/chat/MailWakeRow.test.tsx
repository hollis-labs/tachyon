import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ThreadView } from './ThreadView'
import type { ChatMessage } from './types'

afterEach(cleanup)

const msg = (id: string, at: number, over: Partial<ChatMessage> = {}): ChatMessage => ({ id, threadId: 't', from: 'agent', body: `Reply ${id}`, at, tools: [], ...over })

describe('mail-wake folding', () => {
  it('folds a run of mail-wake replies into one row that opens to the replies', () => {
    render(<ThreadView loading={false} hasMore={false} onLoadOlder={() => {}} agentLabel="archie" messages={[
      msg('a', 1, { from: 'operator', body: 'Ship it' }),
      msg('w1', 2, { wake: 'mail', body: '## Inbox\nRead 1 message from @photon-review.' }),
      msg('w2', 3, { wake: 'mail', body: 'Merged #689.\nDeploying.' }),
      msg('b', 4, { body: 'Deployed.' }),
      msg('w3', 5, { wake: 'mail', body: 'Nothing actionable.' }),
    ]} />)
    const rows = screen.getAllByRole('button', { name: /handled mail/ })
    expect(rows).toHaveLength(2) // the reply between them splits the runs
    expect(rows[0].textContent).toContain('archie handled mail · 2 turns')
    expect(rows[0].textContent).toContain('Merged #689.') // the newest reply's first line
    expect(rows[1].textContent).toContain('archie handled mail')
    expect(rows[1].textContent).not.toContain('turns')
    expect(screen.queryByText('Deploying.')).toBeNull()
    expect(screen.getByText('Deployed.')).toBeTruthy() // an ordinary reply is never folded
    expect(screen.getByText('Ship it')).toBeTruthy()
    fireEvent.click(rows[0])
    expect(rows[0].getAttribute('aria-expanded')).toBe('true')
    const opened = rows[0].closest('[data-folded="mail-wake"]')?.textContent ?? ''
    expect(opened).toContain('Read 1 message from')
    expect(opened).toContain('Deploying.')
  })

  it('shows a streaming wake reply as still responding', () => {
    render(<ThreadView loading={false} hasMore={false} onLoadOlder={() => {}} messages={[msg('w', 1, { wake: 'mail', streaming: true, body: '' })]} />)
    expect(screen.getByRole('button', { name: /handled mail/ }).textContent).toContain('Responding')
  })
})
