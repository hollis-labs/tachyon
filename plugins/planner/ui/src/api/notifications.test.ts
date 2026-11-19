import { describe, expect, it, vi } from 'vitest'
import { operatorElevation } from '@console/operator/elevation'
import { PlannerApiError } from './errors'
import { authorizing } from './notifications'

const refused = () => new PlannerApiError(403, 'Enter your operator key to continue.', 'assurance_insufficient')

describe('authorizing a Planner write', () => {
  it('asks for operator access once and sends the same write again', async () => {
    const ensure = vi.spyOn(operatorElevation, 'ensure').mockResolvedValue(true)
    const write = vi.fn().mockRejectedValueOnce(refused()).mockResolvedValueOnce('ok')
    expect(await authorizing(write)).toBe('ok')
    expect(ensure).toHaveBeenCalledTimes(1)
    expect(write).toHaveBeenCalledTimes(2)
    ensure.mockRestore()
  })
  it('keeps the original refusal when the prompt is declined, and never retries other errors', async () => {
    const ensure = vi.spyOn(operatorElevation, 'ensure').mockResolvedValue(false)
    const declined = vi.fn().mockRejectedValue(refused())
    await expect(authorizing(declined)).rejects.toMatchObject({ code: 'assurance_insufficient' })
    expect(declined).toHaveBeenCalledTimes(1)
    const conflict = vi.fn().mockRejectedValue(new PlannerApiError(409, 'changed', 'conflict'))
    await expect(authorizing(conflict)).rejects.toMatchObject({ code: 'conflict' })
    expect(conflict).toHaveBeenCalledTimes(1)
    expect(ensure).toHaveBeenCalledTimes(1)
    ensure.mockRestore()
  })
})
