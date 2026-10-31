import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const open = vi.fn()
vi.mock('../../autolink/context', () => ({ useAutolink: () => ({ openDrawer: open }) }))
const list = vi.fn(async (_root: string, path: string) => path === '' ? { root: 'r', path: '', truncated: false, entries: [{ name: 'apps', kind: 'dir' as const }, { name: 'README.md', kind: 'file' as const }] } : { root: 'r', path, truncated: false, entries: [{ name: 'Chat.tsx', kind: 'file' as const }] })
vi.mock('../../files/api', () => ({ fileApi: { roots: async () => ({ roots: [{ id: 'r', label: 'hollis-labs', path_display: '/home/c/dev' }], caps: {} }), list: (r: string, p: string) => list(r, p) } }))
import { FileTree } from './FileTree'

afterEach(() => { cleanup(); open.mockClear() })

describe('project file tree', () => {
  it('expands roots and directories lazily and opens a file in the File viewer', async () => {
    render(<FileTree filter="" />)
    fireEvent.click(await screen.findByRole('treeitem', { name: /hollis-labs/ }))
    fireEvent.click(await screen.findByRole('treeitem', { name: 'apps' }))
    fireEvent.click(await screen.findByRole('treeitem', { name: 'Chat.tsx' }))
    expect(list).toHaveBeenCalledWith('r', '')
    expect(list).toHaveBeenCalledWith('r', 'apps')
    expect(open).toHaveBeenCalledWith({ kind: 'path', canonical: '/home/c/dev/apps/Chat.tsx' })
  })
})
