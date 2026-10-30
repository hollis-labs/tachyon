// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConsolePrefsPanel } from './ConsolePrefsPanel'
import { DEFAULT_CONSOLE_PREFS } from './types'

afterEach(cleanup)

describe('ConsolePrefsPanel', () => {
  it('emits patches for each control and states the critical rule', async () => {
    const onChange = vi.fn()
    render(<ConsolePrefsPanel prefs={DEFAULT_CONSOLE_PREFS} onChange={onChange} />)
    expect(screen.getByText(/Critical alerts always pop up/)).toBeTruthy()
    // Base UI's switch toggles through its hidden checkbox.
    const toggle = (name: string) => {
      const sw = screen.getByRole('switch', { name })
      const box = sw.parentElement!.querySelector<HTMLInputElement>('input[type=checkbox]') ?? (sw as HTMLInputElement)
      return act(async () => fireEvent.click(box))
    }
    await toggle('Quiet mode')
    expect(onChange).toHaveBeenLastCalledWith({ quiet: true })
    await toggle('Quiet hours')
    expect(onChange).toHaveBeenLastCalledWith({ quietHoursEnabled: true })
    expect((screen.getByLabelText('Quiet hours start') as HTMLInputElement).disabled).toBe(true)
    await toggle('Pop-up toasts')
    expect(onChange).toHaveBeenLastCalledWith({ toastsEnabled: false })
    fireEvent.click(screen.getByRole('button', { name: '+ Add source' }))
    const muted = screen.getByLabelText('Source to mute')
    fireEvent.change(muted, { target: { value: 'agent-bravo' } })
    fireEvent.keyDown(muted, { key: 'Enter' })
    expect(onChange).toHaveBeenLastCalledWith({ mutedSources: ['agent-bravo'] })
  })
})

describe('Notify me about', () => {
  it('maps stored thresholds to one level and sets both thresholds from it', async () => {
    const { levelOf, levelPatch } = await import('./levels')
    expect(levelOf({ bellMinSeverity: 'critical' })).toBe('blockers')
    expect(levelOf({ bellMinSeverity: 'error' })).toBe('blockers')
    expect(levelOf({ bellMinSeverity: 'warning' })).toBe('warnings')
    expect(levelOf({ bellMinSeverity: 'debug' })).toBe('everything')
    expect(levelPatch('warnings')).toEqual({ bellMinSeverity: 'warning', toastMinSeverity: 'warning' })
  })
  it('shows the level control with a preview line and patches on change', () => {
    const onChange = vi.fn()
    render(<ConsolePrefsPanel prefs={DEFAULT_CONSOLE_PREFS} onChange={onChange} />)
    expect(screen.getByRole('tab', { name: 'Everything' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.getByText(/Toasts for everything/)).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'Blockers only' }))
    expect(onChange).toHaveBeenLastCalledWith({ bellMinSeverity: 'error', toastMinSeverity: 'error' })
  })
})
