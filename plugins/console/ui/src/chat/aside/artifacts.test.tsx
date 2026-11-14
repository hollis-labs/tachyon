import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

vi.mock('../roster', async (orig) => ({ ...(await orig<typeof import('../roster')>()), chatFetch: vi.fn(async (u: string) => (u.endsWith('/workspace') ? new Response('{}') : new Response('<h1>Hi</h1>', { status: 200 }))) }))
vi.mock('../drawer/model', async (orig) => ({
  ...(await orig<typeof import('../drawer/model')>()),
  useWorkspace: () => ({ items: [{ id: 'a', type: 'file', name: 'report.html', mime: 'text/html', shared: false, added_by: 'agent:pm' }, { id: 'u', type: 'file', name: 'mine.txt', shared: false, added_by: 'user' }], loading: false, error: '' }),
}))
import { ArtifactsPanel, artifactKind } from './ArtifactsPanel'

it('classifies artifacts by type, mime and name', () => {
  expect(artifactKind({ type: 'file', name: 'a.png' })).toBe('image')
  expect(artifactKind({ type: 'file', name: 'a', mime: 'text/html' })).toBe('html')
  expect(artifactKind({ type: 'file', name: 'a.mp4' })).toBe('video')
  expect(artifactKind({ type: 'file', name: 'a.md' })).toBe('markdown')
  expect(artifactKind({ type: 'file', name: 'a.bin' })).toBe('other')
})

it('lists only agent artifacts; opening one replaces the list with a sandboxed preview until closed', async () => {
  render(<ArtifactsPanel threadRef="t" doc={null} docPanel={null} onOpenRef={() => undefined} />)
  expect(screen.queryByText('mine.txt')).toBeNull()
  fireEvent.click(screen.getByText('report.html'))
  const frame = await screen.findByTitle('report.html')
  expect(frame.getAttribute('sandbox')).toBe('')
  expect(screen.queryByText('Artifacts')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Back to artifacts' }))
  expect(screen.getByText('report.html')).toBeTruthy()
})
