/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { createDefaultRefRegistry } from '../refs'
import { createMockResolver } from '../refs/test-resolver'
import { AutolinkProvider, Linkified, PreviewCardView } from '.'
import { buildPreviewCard } from '../refs'

afterEach(cleanup)

const TASK = 'CW-20261016-0101'
const fixtures = { [TASK]: { title: 'Reference registry', description: 'Build the registry and its overlay.', status: { label: 'in progress', tone: 'info' as const }, fields: [{ label: 'Priority', value: 'P1' }] } }

function setup(text: string, resolvers: Record<string, ReturnType<typeof createMockResolver>>) {
  const registry = createDefaultRefRegistry()
  return render(<MemoryRouter><AutolinkProvider registry={registry} resolvers={resolvers}><Linkified text={text} /></AutolinkProvider></MemoryRouter>)
}

it('the hovercard has a Details button that opens the same overlay as a click, with the contents', async () => {
  setup(TASK, { 'torque.task': createMockResolver(fixtures) })
  const chip = document.querySelector('[data-ref-kind]') as HTMLElement
  await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
  act(() => chip.focus())
  const hover = await screen.findByTestId('ref-hovercard')
  fireEvent.click(Array.from(hover.querySelectorAll('button')).find((b) => b.textContent === 'Details')!)
  const drawer = await screen.findByTestId('ref-drawer')
  expect(drawer.textContent).toContain('Reference registry')
  expect(drawer.textContent).toContain('Build the registry and its overlay.')
})
it('kinds that hide Open page still have Details', async () => {
  const { registerTaskRefs } = await import('./TaskDetailPane')
  registerTaskRefs() // torque.task is read in place: no Open page action
  setup(TASK, { 'torque.task': createMockResolver(fixtures) })
  const chip = document.querySelector('[data-ref-kind]') as HTMLElement
  await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
  act(() => chip.focus())
  const hover = await screen.findByTestId('ref-hovercard')
  const labels = Array.from(hover.querySelectorAll('button, a')).map((e) => e.textContent)
  expect(labels).toContain('Details')
  expect(labels).not.toContain('Open page')
})
it('no Details button while loading, for a missing object, or outside a provider', () => {
  const registry = createDefaultRefRegistry()
  const ref = { kind: 'torque.task', canonical: TASK }
  const card = (state: Parameters<typeof buildPreviewCard>[2]) => buildPreviewCard(registry, ref, state)
  const { unmount } = render(<MemoryRouter><AutolinkProvider registry={registry}><PreviewCardView card={card({ status: 'loading' })} /></AutolinkProvider></MemoryRouter>)
  expect(screen.queryByText('Details')).toBeNull()
  unmount()
  render(<MemoryRouter><AutolinkProvider registry={registry}><PreviewCardView card={card({ status: 'not-found', fetchedAt: 0 })} /></AutolinkProvider></MemoryRouter>)
  expect(screen.queryByText('Details')).toBeNull()
  cleanup()
  render(<PreviewCardView card={card({ status: 'found', entity: { title: 'x' }, fetchedAt: 0 })} />)
  expect(screen.queryByText('Details')).toBeNull()
})
