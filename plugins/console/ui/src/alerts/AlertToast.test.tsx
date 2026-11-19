// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'
import { AlertToast } from './AlertToast'
import type { Alert } from './types'

afterEach(cleanup)
const alert = (severity: Alert['severity']): Alert => ({ id: 'a', severity, icon: 'info', title: 'Thing happened', source: 'agent', ts: 0, kind: 'k' })
const show = (severity: Alert['severity']) => render(<MemoryRouter><AlertToast alert={alert(severity)} now={1000} onClose={() => undefined} /></MemoryRouter>)

describe('<AlertToast>', () => {
  it('counts down with a hairline that pauses on hover, and critical Stays until you dismiss it with no countdown', () => {
    show('warning')
    const bar = screen.getByTestId('alert-countdown')
    expect(bar.style.animationPlayState).toBe('running')
    fireEvent.mouseEnter(screen.getByTestId('alert-toast'))
    expect(screen.getByTestId('alert-countdown').style.animationPlayState).toBe('paused')
    cleanup()
    show('critical')
    expect(screen.queryByTestId('alert-countdown')).toBeNull()
    expect(screen.getByText('Stays until you dismiss it')).toBeTruthy()
    expect(screen.getByRole('alert')).toBeTruthy()
  })
})
