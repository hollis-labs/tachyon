/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ChatThread } from '../types'
import { ChatPalette } from './Palette'

afterEach(cleanup)

const active = { id: 't', title: 'Portfolio Manager', agent: { address: 'a', label: 'Portfolio Manager' }, capability: { mode: 'session' }, unread: 0, lastAt: 0 } as unknown as ChatThread

describe('ChatPalette create rows', () => {
  it('drafts a task request from what was typed', () => {
    const onDraft = vi.fn()
    render(<ChatPalette open onOpenChange={() => undefined} a={{ threads: [], active, onOpenThread: () => undefined, startOptions: [], onDraft }} />)
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'ship the vocabulary' } })
    fireEvent.click(screen.getByText('Ask the agent to create a task'))
    expect(onDraft).toHaveBeenCalledWith('Create a task: ship the vocabulary')
  })
})
