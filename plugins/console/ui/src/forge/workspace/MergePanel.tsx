import { Button, Dialog, DialogContent, DialogDescription, DialogTitle } from '@hollis-labs/design-components'
import { useRef, useState } from 'react'
import type { MergeInput, MergeMethod, PullRequest } from '../types'
import type { WriteResult } from './useWorkspace'

/** Why a merge cannot go ahead (hard) or deserves a look first (soft). */
export function mergeBlockers(pr: PullRequest): { hard: string[]; soft: string[] } {
  const hard: string[] = []
  const soft: string[] = []
  if (pr.state !== 'open') hard.push(`The pull request is ${pr.state}.`)
  if (pr.draft) hard.push('It is still a draft.')
  if (!pr.headSha) hard.push('The head commit is unavailable; refresh the pull request.')
  if (pr.mergeable === 'conflicting') hard.push('It has merge conflicts with the base branch.')
  if (pr.mergeable === 'blocked') hard.push('Branch rules block this merge (required reviews or checks).')
  const failing = pr.checks.filter((c) => c.status === 'completed' && !['success', 'skipped', 'neutral'].includes(c.conclusion ?? ''))
  if (failing.length) soft.push(`${failing.length} check${failing.length === 1 ? ' is' : 's are'} failing: ${failing.map((c) => c.name).join(', ')}.`)
  const running = pr.checks.filter((c) => c.status !== 'completed')
  if (running.length) soft.push(`${running.length} check${running.length === 1 ? ' is' : 's are'} still running.`)
  if (pr.reviews.some((r) => r.state === 'changes_requested')) soft.push('A reviewer requested changes.')
  return { hard, soft }
}

// Merge dialog: states the exact commit and branches, lists what blocks or warrants a look, and
// merges only on an explicit confirm.
export function MergePanel({ pr, onClose, onSubmit, onBusyChange }: { pr: PullRequest; onClose(): void; onBusyChange(busy: boolean): void; onSubmit(input: MergeInput): Promise<WriteResult> }) {
  const [method, setMethod] = useState<MergeMethod>('merge')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  const { hard, soft } = mergeBlockers(pr)
  const blocked = hard.length > 0
  async function submit() {
    if (inFlight.current || blocked) return
    inFlight.current = true
    setBusy(true)
    onBusyChange(true)
    setError('')
    try {
      const result = await onSubmit({ method, headSha: pr.headSha! })
      if (result.ok) onClose()
      else setError(result.message || 'Merge was not completed')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Merge was not completed')
    } finally {
      inFlight.current = false
      setBusy(false)
      onBusyChange(false)
    }
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}>
      <DialogContent aria-label="Merge pull request" className="sm:max-w-lg">
        <DialogTitle>Merge #{pr.ref.number}</DialogTitle>
        <DialogDescription>
          <span className="font-mono">{pr.headBranch}</span> into <span className="font-mono">{pr.baseBranch}</span> · commit <span className="font-mono">{pr.headSha?.slice(0, 12) ?? 'unavailable'}</span>
        </DialogDescription>
        {blocked && (
          <div role="alert" className="border border-danger/40 px-3 py-2">
            <p className="dash-control font-medium text-danger">Cannot merge yet</p>
            <ul className="mt-1 list-disc pl-5 dash-support text-fg">{hard.map((h) => <li key={h}>{h}</li>)}</ul>
          </div>
        )}
        {soft.length > 0 && (
          <div className="border border-border px-3 py-2">
            <p className="dash-control font-medium text-fg">Before you merge</p>
            <ul className="mt-1 list-disc pl-5 dash-support text-fg-muted">{soft.map((s) => <li key={s}>{s}</li>)}</ul>
          </div>
        )}
        <label className="block dash-control text-fg-muted">Merge method
          <select value={method} onChange={e => setMethod(e.target.value as MergeMethod)} disabled={busy} className="ml-2 border border-border bg-bg px-2 py-1.5 text-fg">
            <option value="merge">Create a merge commit</option>
            <option value="squash">Squash and merge</option>
            <option value="rebase">Rebase and merge</option>
          </select>
        </label>
        <p className="dash-support text-fg-faint">The forge also checks repository permissions, allowed merge methods and branch rules. This merges without submitting a review.</p>
        {error && <p role="alert" className="dash-support text-danger">{error}. Refresh the pull request to check its current state before retrying.</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Merging…' : 'Confirm merge'}</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
