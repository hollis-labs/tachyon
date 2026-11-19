import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AsideProvider, useAside } from './context'
import { alwaysGrants, resetAllGrants, resetGrant } from './grants'
import { ActivityPanel, ApprovalsPanel, PlanPanel, TopicsPanel, WorkPanel } from './Panels'
import { TabStrip } from './TabStrip'
import { DEMO_ACTIVITY, DEMO_PLAN } from './fixtures'
import type { Plan } from './data'

afterEach(cleanup)

let api!: ReturnType<typeof useAside>
function Harness() {
  api = useAside()
  return <aside ref={(el) => { api.rootRef.current = el }}><input aria-label="inside" /><span data-testid="p">{api.panel}</span></aside>
}
const mount = () => render(<AsideProvider wide><Harness /></AsideProvider>)

describe('agent-driven aside', () => {
  const ask = (scope = 'pm|t1') => { act(() => api.setScope(scope)) }
  it('the agent opens a panel at once when the user has not changed the view', () => {
    mount(); ask()
    let r = ''
    act(() => { r = api.auto('approvals', 'agent') })
    expect(r).toBe('applied')
    expect(screen.getByTestId('p').textContent).toBe('approvals')
  })
  it('after a manual change the request is held, and Once applies just that request', () => {
    resetAllGrants(); mount(); ask()
    act(() => api.pick('work'))
    let r = ''
    act(() => { r = api.auto('plan', 'agent') })
    expect(r).toBe('pending')
    expect(screen.getByTestId('p').textContent).toBe('work')
    act(() => api.answer('once'))
    expect(screen.getByTestId('p').textContent).toBe('plan')
    act(() => api.pick('work'))
    act(() => { r = api.auto('plan', 'agent') })
    expect(r).toBe('pending')
  })
  it('This session and Always stop the question for that agent in that chat only; Not now drops it', () => {
    resetAllGrants(); mount(); ask('pm|t1')
    act(() => api.pick('work')); act(() => { api.auto('plan', 'agent') }); act(() => api.answer('deny'))
    expect(screen.getByTestId('p').textContent).toBe('work')
    expect(api.pending).toBeNull()
    act(() => { api.auto('plan', 'agent') }); act(() => api.answer('session'))
    act(() => api.pick('work'))
    let r = ''
    act(() => { r = api.auto('files', 'agent') })
    expect(r).toBe('applied')
    ask('pm|t2'); act(() => api.pick('work'))
    act(() => { r = api.auto('files', 'agent') })
    expect(r).toBe('pending')
    act(() => api.answer('always'))
    expect(alwaysGrants()).toEqual(['pm|t2'])
    act(() => resetGrant('pm|t2'))
    expect(alwaysGrants()).toEqual([])
  })
  it('never switches while the user is typing in the aside, and never takes focus', () => {
    mount(); ask()
    const input = screen.getByLabelText('inside')
    input.focus()
    act(() => { api.auto('approvals', 'agent') })
    expect(screen.getByTestId('p').textContent).toBe('plan')
    expect(document.activeElement).toBe(input)
  })
})

describe('panels', () => {
  it('plan: empty copy with one action, and a step toggles', () => {
    const onAsk = vi.fn()
    const p = { plans: [] as Plan[], loading: false, error: '', setStatus: vi.fn(), addStep: vi.fn(), createPlan: vi.fn(), deletePlan: vi.fn(), reorderPlans: vi.fn(), moveStep: vi.fn(), onAsk }
    const { rerender } = render(<PlanPanel {...p} />)
    expect(screen.getByText(/No plan yet/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ask the agent to draft one' }))
    expect(onAsk).toHaveBeenCalled()
    const setStatus = vi.fn()
    rerender(<PlanPanel {...p} plans={[DEMO_PLAN]} setStatus={setStatus} />)
    expect(screen.getByRole('progressbar', { name: '5 of 8 done' })).toBeTruthy()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Run the Parallax spike' }))
    expect(setStatus).toHaveBeenCalledWith(DEMO_PLAN, expect.objectContaining({ title: 'Run the Parallax spike' }), 'done')
  })
  it('plan: several plans collapse, reorder with buttons and move a step', () => {
    const mk = (id: string, title: string, steps: string[]): Plan => ({ ...DEMO_PLAN, id, title, task_ref: null, steps: steps.map((t, i) => ({ ...DEMO_PLAN.steps[0], id: `${id}-${i}`, title: t, status: 'todo' })) } as Plan)
    const a = mk('a', 'Alpha', ['one']), b = mk('b', 'Beta', ['two'])
    const reorderPlans = vi.fn(), moveStep = vi.fn()
    render(<PlanPanel plans={[a, b]} loading={false} error="" setStatus={vi.fn()} addStep={vi.fn()} createPlan={vi.fn()} deletePlan={vi.fn()} reorderPlans={reorderPlans} moveStep={moveStep} onAsk={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Move Alpha down' }))
    expect(reorderPlans).toHaveBeenCalledWith(['b', 'a'])
    fireEvent.change(screen.getByLabelText('Move one to another plan'), { target: { value: 'b' } })
    expect(moveStep).toHaveBeenCalledWith(a, b, 'a-0')
    fireEvent.click(screen.getByRole('button', { name: /^Alpha/ }))
    expect(screen.queryByRole('checkbox', { name: 'one' })).toBeNull()
  })
  it('topics and approvals have one-line empty states', () => {
    const { unmount } = render(<TopicsPanel marks={{ bookmarks: [], toggleBookmark: vi.fn(), topics: [], addTopic: vi.fn() }} />)
    expect(screen.getByText(/No topics yet/)).toBeTruthy()
    unmount()
    render(<ApprovalsPanel blocks={[]} agent="A" rules={[]} revoke={vi.fn()} />)
    expect(screen.getByText(/Approval queue · 0 waiting/)).toBeTruthy()
  })
  it('activity: quoted comment, mini card, search and a posted comment keeps text on failure', async () => {
    const comment = vi.fn().mockResolvedValue(false)
    render(<ActivityPanel events={DEMO_ACTIVITY.activity} hasMore loading={false} error="" comment={comment} />)
    expect(screen.getByText('Waiting on you: pick a path for generative UI.').tagName).toBe('BLOCKQUOTE')
    expect(screen.getByText('Wire the registry v2 kinds')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Search activity'), { target: { value: 'registry' } })
    expect(screen.queryByText('Waiting on you: pick a path for generative UI.')).toBeNull()
    fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'hello' } })
    await act(async () => { fireEvent.submit(screen.getByLabelText('Comment').closest('form')!) })
    expect((screen.getByLabelText('Comment') as HTMLInputElement).value).toBe('hello')
  })
})

describe('work panel', () => {
  it('draws one segment per status when a row carries counts, and a plain done/todo bar otherwise', () => {
    render(<WorkPanel loading={false} error="" source="planner" items={[
      { id: 'CW-1', kind: 'task', title: 'Plan task', status: 'doing', progress: 0.5, counts: { done: 2, doing: 1, blocked: 1, todo: 0 }, updated_at: 0 },
      { id: 'TH-1', kind: 'theme', title: 'Theme', status: 'doing', progress: 0.25, updated_at: 0 },
    ]} />)
    const [withCounts, plain] = screen.getAllByRole('progressbar')
    expect(withCounts.getAttribute('aria-valuemax')).toBe('4')
    expect(withCounts.getAttribute('aria-valuenow')).toBe('2')
    expect(withCounts.children.length).toBe(3)
    expect(plain.children.length).toBe(2)
    // The plan task has its own bar, so it is not repeated under Doing.
    expect(screen.queryByText('Doing')).toBeNull()
  })
})

describe('tab strip', () => {
  const dims = (el: HTMLElement, o: { scrollWidth: number; clientWidth: number; scrollLeft?: number }) => {
    Object.defineProperty(el, 'scrollWidth', { configurable: true, value: o.scrollWidth })
    Object.defineProperty(el, 'clientWidth', { configurable: true, value: o.clientWidth })
    el.scrollLeft = o.scrollLeft ?? 0
  }
  const strip = () => render(<TabStrip label="Panels" activeKey="a"><button role="tab" aria-selected="true">A</button><button role="tab" aria-selected="false">B</button></TabStrip>)

  it('keeps one line and shows no arrows when nothing overflows', () => {
    strip()
    const list = screen.getByRole('tablist')
    expect(list.className).toContain('flex-nowrap')
    dims(list, { scrollWidth: 200, clientWidth: 200 })
    fireEvent.scroll(list)
    expect((screen.getByTestId('tabs-left') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('tabs-right') as HTMLButtonElement).disabled).toBe(true)
  })
  it('enables the arrow for each direction that has more tabs, and scrolls on click', () => {
    strip()
    const list = screen.getByRole('tablist')
    list.scrollTo = vi.fn() as unknown as typeof list.scrollTo
    dims(list, { scrollWidth: 600, clientWidth: 200 })
    fireEvent.scroll(list)
    expect((screen.getByTestId('tabs-left') as HTMLButtonElement).disabled).toBe(true)
    const right = screen.getByRole('button', { name: 'Scroll tabs right' }) as HTMLButtonElement
    expect(right.disabled).toBe(false)
    fireEvent.click(right)
    expect(list.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 140 }))
    dims(list, { scrollWidth: 600, clientWidth: 200, scrollLeft: 200 })
    fireEvent.scroll(list)
    expect((screen.getByTestId('tabs-left') as HTMLButtonElement).disabled).toBe(false)
    expect((screen.getByTestId('tabs-right') as HTMLButtonElement).disabled).toBe(false)
    dims(list, { scrollWidth: 600, clientWidth: 200, scrollLeft: 400 })
    fireEvent.scroll(list)
    expect((screen.getByTestId('tabs-right') as HTMLButtonElement).disabled).toBe(true)
  })
})
