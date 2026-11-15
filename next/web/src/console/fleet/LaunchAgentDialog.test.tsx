import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { LaunchAgentDialog } from './LaunchAgentDialog'
import { launchAgent, launchOptions, LaunchRequestError } from './launch-client'
import type { ReactNode } from 'react'
// Isolate request recovery from floating-position geometry; native dialog and
// combobox scrolling/keyboard behavior is also verified in the browser.
vi.mock('@hollis-labs/design-components', async importOriginal => ({
  ...await importOriginal<typeof import('@hollis-labs/design-components')>(),
  FormDialog: ({ open, onClose, onSubmit, submitDisabled, submitting, submitLabel, children }: { open: boolean; onClose: () => void; onSubmit: () => void; submitDisabled: boolean; submitting: boolean; submitLabel: string; children: ReactNode }) => open ? <div><button onClick={onClose}>Cancel</button>{children}<button disabled={submitDisabled || submitting} onClick={onSubmit}>{submitLabel}</button></div> : null,
  InputCombobox: ({ label, value, onChange, items }: { label: string; value: string; onChange: (value: string) => void; items: {id:string;label:string}[] }) => <select aria-label={label} value={value ?? ''} onChange={e => onChange(e.target.value)}><option value="">Choose</option>{items.map(i => <option key={i.id} value={i.id}>{i.label}</option>)}</select>,
}))
vi.mock('./launch-client', async importOriginal => ({ ...await importOriginal<typeof import('./launch-client')>(), launchOptions: vi.fn(), launchAgent: vi.fn() }))
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks() })
function setup() {
  Element.prototype.scrollIntoView = vi.fn()
  vi.mocked(launchOptions).mockResolvedValue({ profiles: [{ id:'review', agent:'reviewer', provider:'codex', project:'test' }], workers:[] })
  vi.mocked(launchAgent).mockRejectedValue(new LaunchRequestError('Session creation failed (500)',500))
  const onClose=vi.fn(), onLaunched=vi.fn()
  return { ...render(<LaunchAgentDialog open onClose={onClose} onLaunched={onLaunched} />), onClose, onLaunched }
}
async function fill() {
  await screen.findByRole('option',{name:'review'})
  fireEvent.change(screen.getByLabelText('Launch profile'),{target:{value:'review'}})
  fireEvent.change(screen.getByLabelText('Boot prompt (optional)'),{target:{value:'Review this change'}})
}
it('clears failed fields and warning when closed, but recovers the same request if reentered', async () => {
  const view=setup()
  await fill()
  fireEvent.click(screen.getByRole('button',{name:'Launch'}))
  await screen.findByRole('button',{name:'Retry launch'})
  const original=vi.mocked(launchAgent).mock.calls[0][0].input.idempotency_key
  fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
  expect(view.onClose).toHaveBeenCalledOnce()
  view.rerender(<LaunchAgentDialog open={false} onClose={view.onClose} onLaunched={view.onLaunched} />)
  view.rerender(<LaunchAgentDialog open onClose={view.onClose} onLaunched={view.onLaunched} />)
  expect(screen.queryByRole('alert')).toBeNull()
  expect((screen.getByLabelText('Boot prompt (optional)') as HTMLTextAreaElement).value).toBe('')
  expect(screen.queryByRole('button',{name:'Retry launch'})).toBeNull()
  await fill()
  await waitFor(()=>expect((screen.getByRole('button',{name:'Launch'}) as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(screen.getByRole('button',{name:'Launch'}))
  await waitFor(()=>expect(launchAgent).toHaveBeenCalledTimes(2))
  expect(vi.mocked(launchAgent).mock.calls[1][0].input.idempotency_key).toBe(original)
})
it('allows editing after failure and Clear form removes prior selection and retry warning', async () => {
  setup(); await fill()
  fireEvent.click(screen.getByRole('button',{name:'Launch'}))
  await screen.findByRole('alert')
  expect((screen.getByLabelText('Boot prompt (optional)') as HTMLTextAreaElement).disabled).toBe(false)
  fireEvent.click(screen.getByRole('button',{name:'Clear form'}))
  expect(screen.queryByRole('alert')).toBeNull()
  expect((screen.getByLabelText('Launch profile') as HTMLSelectElement).value).toBe('')
  expect((screen.getByLabelText('Boot prompt (optional)') as HTMLTextAreaElement).value).toBe('')
  expect(screen.queryByRole('button',{name:'Retry launch'})).toBeNull()
})
