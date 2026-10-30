import { Button, Callout, Dialog, DialogContent, DialogTitle } from '@hollis-labs/design-components'
import { useRef, useState } from 'react'
import type { MergeInput, MergeMethod, PullRequest } from '../types'
import type { WriteResult } from './useWorkspace'

/** Why a merge cannot go ahead (hard) or deserves a look first (soft). */
export function mergeBlockers(pr: PullRequest): { hard: string[]; soft: string[] } {
  const hard: string[] = []
  const soft: string[] = []
  if (pr.state !== 'open') hard.push(`The pull request is ${pr.state}.`)
  if (pr.draft) hard.push('This pull request is a draft.')
  if (!pr.headSha) hard.push('The head commit is unavailable; refresh the pull request.')
  if (pr.mergeable === 'conflicting') hard.push(`It has merge conflicts with ${pr.baseBranch}.`)
  if (pr.mergeable === 'blocked') hard.push('Branch rules block this merge (required reviews or checks).')
  const failing = pr.checks.filter((c) => c.status === 'completed' && !['success', 'skipped', 'neutral'].includes(c.conclusion ?? ''))
  if (failing.length) hard.push(`${failing.length} of ${pr.checks.length} checks failed.`)
  const running = pr.checks.filter((c) => c.status !== 'completed')
  if (running.length) soft.push(`${running.length} check${running.length === 1 ? ' is' : 's are'} still running.`)
  if (pr.reviews.some((r) => r.state === 'changes_requested')) soft.push('A reviewer requested changes.')
  return { hard, soft }
}

const METHODS: { value: MergeMethod; label: string }[] = [
  { value: 'merge', label: 'Create a merge commit' },
  { value: 'squash', label: 'Squash and merge' },
  { value: 'rebase', label: 'Rebase and merge' },
]

// Merge dialog: states the exact commit and branches, lists what blocks or warrants a look, and
// merges only on an explicit confirm.
export function MergePanel({ pr, fileCount, onClose, onSubmit, onBusyChange }: { pr: PullRequest; fileCount: number; onClose(): void; onBusyChange(busy: boolean): void; onSubmit(input: MergeInput): Promise<WriteResult> }) {
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
  const files = `${fileCount} ${fileCount === 1 ? 'file' : 'files'}`
  const effect = {
    merge: `Create a merge commit with ${files}, +${pr.additions ?? 0} −${pr.deletions ?? 0}.`,
    squash: `Squash ${files}, +${pr.additions ?? 0} −${pr.deletions ?? 0} into one commit on ${pr.baseBranch}.`,
    rebase: `Replay the commits of ${pr.headBranch} onto ${pr.baseBranch}, ${files}, +${pr.additions ?? 0} −${pr.deletions ?? 0}.`,
  }[method]
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose() }}>
      <DialogContent aria-label="Merge pull request" className="flex flex-col gap-4 sm:max-w-xl">
        <DialogTitle className="font-sans text-base font-semibold normal-case tracking-normal text-fg">Merge #{pr.ref.number}</DialogTitle>
        <div className="divide-y divide-border rounded border border-border text-control">
          <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-2.5 font-mono">
            <span className="min-w-0 [overflow-wrap:anywhere]">{pr.headBranch} <span className="text-fg-muted">{'→'}</span> {pr.baseBranch}</span>
            <span className="text-xs text-fg-muted">commit {pr.headSha?.slice(0, 12) ?? 'unavailable'}</span>
          </div>
          <p className="px-3 py-2.5 text-fg-muted">{effect}</p>
        </div>
        <fieldset disabled={busy}>
          <legend className="mb-2 font-mono text-caption uppercase tracking-label text-fg-muted">Merge method</legend>
          <div role="radiogroup" aria-label="Merge method" className="space-y-1.5">
            {METHODS.map((m) => (
              <label key={m.value} className={`flex cursor-pointer items-center gap-3 rounded border border-border px-3 py-2.5 text-control ${method === m.value ? 'bg-surface-hover' : 'hover:bg-surface/40'}`}>
                <input type="radio" name="merge-method" value={m.value} checked={method === m.value} onChange={() => setMethod(m.value)} className="accent-primary" />
                <span className="font-medium text-fg">{m.label}</span>
              </label>
            ))}
          </div>
        </fieldset>
        {blocked && (
          <Callout tone="danger" className="[&_ul]:list-disc [&_ul]:pl-5">
            <p><span className="font-medium">Blocked.</span> Fix these before merging, then refresh.</p>
            <ul className="mt-1">{hard.map((h) => <li key={h}>{h}</li>)}</ul>
          </Callout>
        )}
        {soft.length > 0 && (
          <Callout tone="neutral" title="Before you merge" className="[&_ul]:list-disc [&_ul]:pl-5">
            <ul>{soft.map((x) => <li key={x}>{x}</li>)}</ul>
          </Callout>
        )}
        {!blocked && <p className="text-xs text-fg-muted">The forge still checks repository permissions, allowed methods and branch rules. This merges without submitting a review.</p>}
        {error && <p role="alert" className="text-xs text-danger">{error}. Refresh the pull request to check its current state before retrying.</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Merging…' : 'Confirm merge'}</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
