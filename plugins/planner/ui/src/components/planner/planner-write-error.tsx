import { useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { operatorElevation } from '@console/operator/elevation'
import { isConflict, needsOperator, plannerWriteMessage } from '@planner/api/errors'

export function PlannerWriteError({ error, onReload }: { error: unknown; onReload?: () => void }) {
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  if (!error) return null
  async function authorize() {
    setBusy(true)
    setMessage('')
    try {
      if (await operatorElevation.ensure()) setMessage('Operator access verified. Review your draft and submit again.')
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Operator authorization failed.')
    } finally { setBusy(false) }
  }
  return <div role="alert" className="flex flex-col gap-2 border border-warning/40 bg-warning/5 p-3 dash-body">
    <p>{plannerWriteMessage(error)}</p>
    {message && <p>{message}</p>}
    <div className="flex gap-2">
      {needsOperator(error) && <Button size="sm" variant="outline" disabled={busy} onClick={authorize}>Authorize operator</Button>}
      {isConflict(error) && onReload && <Button size="sm" variant="outline" onClick={onReload}>Reload latest version</Button>}
    </div>
  </div>
}
