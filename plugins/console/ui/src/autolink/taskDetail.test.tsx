/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Comment, ListPage, Task } from '@tachyon/torque-client'
import { createDefaultRefRegistry } from '../refs'
import { createMockResolver } from '../refs/test-resolver'
import { AutolinkProvider, Linkified } from '.'
import { registerTaskRefs } from './TaskDetailPane'
import { createTaskDetailCache, TaskDetailContext, type TaskDetailApi } from './taskDetail'

afterEach(cleanup)

const ID = 'CW-20261016-0101'
const task = (over: Partial<Task> = {}): Task => ({
  id: ID, title: 'Reference registry', description: 'Build the registry.\nSee CW-20261016-0102.', status: 'doing', priority: 2,
  tags: [{ slug: 'chat' } as Task['tags'][number]], executor: 'cli', kind: 'agent', depends_on: [], blocked_reason: '',
  project_id: 'PRJ-1', epic_id: null, sprint_id: null, created_at: '2026-10-15T00:00:00Z', updated_at: '2026-10-16T05:00:00Z', ...over,
} as Task)
const comment = (id: number, content: string): Comment => ({ id, entity_type: 'task', entity_id: ID, author: 'reviewer', content, created_at: '2026-10-16T06:00:00Z' })
const page = (items: Comment[], total?: number): ListPage<Comment> => ({ items, meta: { returned: items.length, limit: 25, has_more: total !== undefined && total > items.length, next_cursor: null, ...(total === undefined ? {} : { total }) } })

function fakeApi(over: Partial<TaskDetailApi> = {}) {
  const getTask = vi.fn(async () => task())
  const listComments = vi.fn(async () => page([comment(2, 'newer'), comment(1, 'older')], 2))
  return { api: { getTask, listComments, ...over } as TaskDetailApi, getTask, listComments }
}

describe('task detail cache', () => {
  it('serves one fetch to every reader inside the TTL and refetches after it', async () => {
    let t = 0
    const { api, getTask } = fakeApi()
    const cache = createTaskDetailCache(api, { ttlMs: 1000, now: () => t })
    cache.ensure(ID)
    cache.ensure(ID)
    await vi.waitFor(() => expect(cache.get(ID)?.phase).toBe('ready'))
    expect(getTask).toHaveBeenCalledTimes(1)
    t = 999
    cache.ensure(ID)
    expect(getTask).toHaveBeenCalledTimes(1)
    t = 1001
    cache.ensure(ID)
    await vi.waitFor(() => expect(getTask).toHaveBeenCalledTimes(2))
    // Stale data stays visible while it refreshes.
    expect(cache.get(ID)?.task?.title).toBe('Reference registry')
  })

  it('counts comments from the reported total and flags a truncated list', async () => {
    const { api } = fakeApi({ listComments: vi.fn(async () => page([comment(2, 'a'), comment(1, 'b')], 40)) })
    const cache = createTaskDetailCache(api)
    cache.ensure(ID)
    await vi.waitFor(() => expect(cache.get(ID)?.phase).toBe('ready'))
    expect(cache.get(ID)).toMatchObject({ commentCount: 40, moreComments: true, commentsFailed: false })
  })

  it('still shows the task when comments cannot be read', async () => {
    const { api } = fakeApi({ listComments: vi.fn(async () => { throw new Error('boom') }) })
    const cache = createTaskDetailCache(api)
    cache.ensure(ID)
    await vi.waitFor(() => expect(cache.get(ID)?.phase).toBe('ready'))
    expect(cache.get(ID)).toMatchObject({ commentsFailed: true, comments: [] })
    expect(cache.get(ID)?.task?.id).toBe(ID)
  })

  it('keeps the last copy when a refresh fails, and invalidate forgets it', async () => {
    let fail = false
    const { api } = fakeApi({ getTask: vi.fn(async () => { if (fail) throw new Error('down'); return task() }) })
    let t = 0
    const cache = createTaskDetailCache(api, { ttlMs: 10, now: () => t })
    cache.ensure(ID)
    await vi.waitFor(() => expect(cache.get(ID)?.phase).toBe('ready'))
    fail = true
    t = 100
    cache.ensure(ID)
    await vi.waitFor(() => expect(cache.get(ID)?.phase).toBe('error'))
    expect(cache.get(ID)).toMatchObject({ error: 'down' })
    expect(cache.get(ID)?.task?.title).toBe('Reference registry')
    cache.invalidate(ID)
    expect(cache.get(ID)).toBeUndefined()
  })
})

function setup(api: TaskDetailApi) {
  registerTaskRefs()
  const cache = createTaskDetailCache(api)
  const resolver = createMockResolver({ [ID]: { title: 'Reference registry', status: { label: 'doing', tone: 'info' as const }, updatedAt: '2026-10-16T05:00:00Z' } })
  render(
    <MemoryRouter>
      <TaskDetailContext.Provider value={cache}>
        <AutolinkProvider registry={createDefaultRefRegistry()} resolvers={{ 'torque.task': resolver }}>
          <Linkified text={ID} chip={{ hoverDelay: 0 }} />
        </AutolinkProvider>
      </TaskDetailContext.Provider>
    </MemoryRouter>,
  )
}

describe('task overlays', () => {
  it('the drawer shows description, metadata and comments, with no Open page', async () => {
    setup(fakeApi().api)
    fireEvent.click(await waitFor(() => { const c = document.querySelector('[data-ref-state="found"]'); if (!c) throw new Error('wait'); return c as HTMLElement }))
    const drawer = await screen.findByTestId('ref-drawer')
    await waitFor(() => expect(drawer.textContent).toContain('Build the registry.'))
    expect(drawer.textContent).toContain('Comments · 2')
    expect(drawer.textContent).toContain('newer')
    expect(drawer.textContent).toContain('older')
    expect(drawer.textContent).toContain('P2')
    expect(drawer.textContent).toContain('PRJ-1')
    expect(drawer.textContent).not.toContain('Open page')
    expect(drawer.textContent).toContain('Copy ID')
  })

  it('the drawer says so when the task cannot be loaded, and Retry refetches', async () => {
    const getTask = vi.fn<TaskDetailApi['getTask']>().mockRejectedValueOnce(new Error('nope')).mockResolvedValue(task())
    setup(fakeApi({ getTask }).api)
    fireEvent.click(await waitFor(() => { const c = document.querySelector('[data-ref-state="found"]'); if (!c) throw new Error('wait'); return c as HTMLElement }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('nope')
    await act(async () => { fireEvent.click(screen.getByText('Retry')) })
    await waitFor(() => expect(screen.getByTestId('ref-drawer').textContent).toContain('Build the registry.'))
  })

  it('the hovercard shows priority, comment count and last update', async () => {
    setup(fakeApi().api)
    const chip = await waitFor(() => { const c = document.querySelector('[data-ref-state="found"]'); if (!c) throw new Error('wait'); return c as HTMLElement })
    fireEvent.focus(chip)
    const meta = await screen.findByTestId('task-hover-meta')
    expect(meta.textContent).toContain('P2')
    expect(meta.textContent).toContain('Comments')
    expect(meta.textContent).toMatch(/Comments\s*2/)
    expect(meta.textContent).toContain('Updated')
  })
})
