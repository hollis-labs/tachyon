import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConversationView } from '../view/ConversationView'

vi.mock('./model', async (orig) => ({
  ...(await orig<typeof import('./model')>()),
  useWorkspace: () => ({ items: [{ id: 'w1', type: 'file', name: 'options-paper.md', shared: true }, { id: 'w2', type: 'link', name: 'golden.html', shared: false }], loading: false, error: '', share: vi.fn(), remove: vi.fn(), addFile: vi.fn(), addLink: vi.fn() }),
  useScratchPlacement: () => 'drawer',
}))
afterEach(cleanup)
const base = { threadId: 't', messages: [], agentLabel: 'Alpha', loading: false, hasMore: false, onLoadOlder: () => undefined, onSend: async () => undefined }

describe('chat drawer', () => {
  it('is hidden until opened, has no tab menu, and the composer icons toggle it', () => {
    render(<ConversationView {...base} />)
    expect(screen.queryByRole('region', { name: /drawer/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Workspace' }))
    const dr = screen.getByRole('region', { name: 'Workspace drawer' })
    expect(dr).toBeTruthy()
    expect(screen.queryByRole('tab')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Terminal' }))
    expect(screen.getByRole('region', { name: 'Terminal drawer' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Terminal' }))
    expect(screen.queryByRole('region', { name: /drawer/ })).toBeNull()
  })

  it('opens with Ctrl+T and Cmd+Shift+W, and closes with Escape back to the composer', () => {
    render(<ConversationView {...base} />)
    fireEvent.keyDown(window, { key: 't', ctrlKey: true })
    expect(screen.getByRole('region', { name: 'Terminal drawer' })).toBeTruthy()
    fireEvent.keyDown(window, { key: 'w', metaKey: true, shiftKey: true })
    expect(screen.getByRole('region', { name: 'Workspace drawer' })).toBeTruthy()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('region', { name: /drawer/ })).toBeNull()
  })

  it('shows shared Workspace items in the composer strip with a count chip', () => {
    render(<ConversationView {...base} />)
    expect(screen.getByRole('button', { name: 'Remove options-paper.md' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Remove golden.html' })).toBeNull()
    expect(screen.getByText('1 shared from Workspace')).toBeTruthy()
  })

  it('resizes the drawer and closes it from its header', () => {
    render(<ConversationView {...base} />)
    fireEvent.click(screen.getByRole('button', { name: 'Scratchpad' }))
    fireEvent.click(screen.getByRole('button', { name: 'Full height' }))
    expect(screen.getByRole('button', { name: 'Full height' }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: 'Close drawer' }))
    expect(screen.queryByRole('region', { name: /drawer/ })).toBeNull()
  })
})

describe('workspace refs', () => {
  it('only an absolute host path is a File viewer ref', async () => {
    const { pathRefOf } = await import('./model')
    expect(pathRefOf({ type: 'ref', ref: '/home/a/b.md' })).toBe('/home/a/b.md')
    expect(pathRefOf({ type: 'ref', ref: 'tach:task/CW-1' })).toBeNull()
    expect(pathRefOf({ type: 'file', ref: '/x' })).toBeNull()
  })
})
