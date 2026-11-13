/** @vitest-environment jsdom */
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { Dialog, DialogContent, DialogTitle } from '@hollis-labs/design-components'
import { RefDrawer } from './RefDrawer'

afterEach(cleanup)

const target = { kind: 'torque.task', canonical: 'CW-20261016-0101' }

// Stacking rule for the shared reference drawer: below dialogs normally, so a dialog opened from inside it
// covers it; on the dialog layer when it opens over an already-open dialog (a chip inside a modal).
it('opens on the dialog layer when a kit Dialog is already open', async () => {
  const ui = (t: typeof target | null) => (
    <>
      <Dialog open><DialogContent><DialogTitle>Session</DialogTitle></DialogContent></Dialog>
      <RefDrawer target={t} onClose={() => undefined} />
    </>
  )
  const { rerender } = render(ui(null))
  await screen.findByRole('dialog', { name: 'Session' })
  rerender(ui(target)) // the chip inside the modal opens the drawer
  const drawer = await screen.findByTestId('ref-drawer')
  expect(drawer.className).toContain('z-[60]')
  expect(drawer.className).not.toMatch(/(^|\s)z-50(\s|$)/)
})

it('stays below dialogs when nothing else is open', async () => {
  render(<RefDrawer target={target} onClose={() => undefined} />)
  const drawer = await screen.findByTestId('ref-drawer')
  expect(drawer.className).toMatch(/(^|\s)z-50(\s|$)/)
})
