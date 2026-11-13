import { describe, expect, it } from 'vitest'
import { previewLine } from './preview'

describe('previewLine', () => {
  it('removes markdown markers and keeps the text', () => {
    expect(previewLine('**The ports are mostly further along than Torq')).toBe('The ports are mostly further along than Torq')
    expect(previewLine('PR #34 is merged as `d349d0e7`, and the **review** is _done_.')).toBe('PR #34 is merged as d349d0e7, and the review is done.')
    expect(previewLine('## Status\n- first [link](https://x.test/a)\n> quoted')).toBe('Status first link quoted')
  })
  it('leaves identifiers and plain text alone', () => {
    expect(previewLine('torque_task_list and snake_case_name * 2')).toBe('torque_task_list and snake_case_name * 2')
    expect(previewLine(undefined)).toBe('')
  })
})
