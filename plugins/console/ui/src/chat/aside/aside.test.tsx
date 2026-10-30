import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AsideProvider, useAside } from './context'
import { ActivityPanel, ApprovalsPanel, PlanPanel, TopicsPanel } from './Panels'
import { DEMO_ACTIVITY, DEMO_PLAN } from './fixtures'

afterEach(cleanup)

let api!: ReturnType<typeof useAside>
function Harness() {
  api = useAside()
  return <aside ref={(el) => { api.rootRef.current = el }}><input aria-label="inside" /><span data-testid="p">{api.panel}</span><span data-testid="m">{api.mode}</span></aside>
}
const mount = () => render(<AsideProvider wide><Harness /></AsideProvider>)

describe('auto aside', () => {
  it('follows the agent in auto mode', () => {
    mount()
    act(() => api.auto('approvals', 'opened by agent'))
    expect(screen.getByTestId('p').textContent).toBe('approvals')
    expect(api.reason).toBe('opened by agent')
  })
  it('a manual pick locks until the user returns to auto', () => {
    mount()
    act(() => api.pick('work'))
    act(() => api.auto('plan', 'opened by agent'))
    expect(screen.getByTestId('p').textContent).toBe('work')
    act(() => api.backToAuto())
    act(() => api.auto('plan', 'opened by agent'))
    expect(screen.getByTestId('p').textContent).toBe('plan')
  })
  it('never switches while the user is typing in the aside, and never takes focus', () => {
    mount()
    const input = screen.getByLabelText('inside')
    input.focus()
    act(() => api.auto('approvals', 'opened by agent'))
    expect(screen.getByTestId('p').textContent).toBe('plan')
    expect(document.activeElement).toBe(input)
  })
})

describe('panels', () => {
  it('plan: empty copy with one action, and a step toggles', () => {
    const onAsk = vi.fn()
    const { rerender } = render(<PlanPanel plan={null} loading={false} error="" setStatus={vi.fn()} addStep={vi.fn()} onAsk={onAsk} />)
    expect(screen.getByText(/No plan yet/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ask the agent to draft one' }))
    expect(onAsk).toHaveBeenCalled()
    const setStatus = vi.fn()
    rerender(<PlanPanel plan={DEMO_PLAN} loading={false} error="" setStatus={setStatus} addStep={vi.fn()} onAsk={onAsk} />)
    expect(screen.getByRole('progressbar', { name: '5 of 8 done' })).toBeTruthy()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Run the Parallax spike' }))
    expect(setStatus).toHaveBeenCalledWith(expect.objectContaining({ title: 'Run the Parallax spike' }), 'done')
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
