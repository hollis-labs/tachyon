// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConsolePrefsPanel } from './ConsolePrefsPanel'
import { DEFAULT_CONSOLE_PREFS } from './types'

afterEach(cleanup)

describe('ConsolePrefsPanel', () => {
  it('emits patches for each control and states the critical rule', () => {
    const onChange = vi.fn()
    render(<ConsolePrefsPanel prefs={DEFAULT_CONSOLE_PREFS} onChange={onChange} />)
    expect(screen.getByText(/Critical alerts still break through/)).toBeTruthy()
    fireEvent.click(screen.getByLabelText('Quiet mode'))
    expect(onChange).toHaveBeenLastCalledWith({ quiet: true })
    fireEvent.click(screen.getByLabelText('Quiet hours'))
    expect(onChange).toHaveBeenLastCalledWith({ quietHoursEnabled: true })
    expect((screen.getByLabelText('Quiet hours start') as HTMLInputElement).disabled).toBe(true)
    const muted = screen.getByPlaceholderText('agent-bravo, laptop')
    fireEvent.change(muted, { target: { value: 'a, b' } })
    expect(onChange).toHaveBeenCalledTimes(2)
    fireEvent.blur(muted)
    expect(onChange).toHaveBeenLastCalledWith({ mutedSources: ['a', ' b'] })
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
    expect(screen.getByText(/Every alert shows in the bell/)).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'Blockers' }))
    expect(onChange).toHaveBeenLastCalledWith({ bellMinSeverity: 'error', toastMinSeverity: 'error' })
  })
})
