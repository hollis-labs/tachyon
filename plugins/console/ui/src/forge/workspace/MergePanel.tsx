import { Button, Callout, Dialog, DialogContent, DialogTitle } from '@hollis-labs/design-components'
import { useEffect, useRef, useState } from 'react'
import { operatorElevation } from '../../operator/elevation'
import type { ForgeClient } from '../client'
import type { MergeInput, MergeMethod, MergePreflight, PullRequest } from '../types'
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

/** One plain sentence per preflight blocker code; unknown codes fall back to the server's message. */
export function blockerSentence(code: string, message: string): string {
  switch (code) {
    case 'draft': return 'This pull request is a draft.'
    case 'closed': return 'This pull request is closed.'
    case 'conflicts': return message || 'The branch has conflicts with the base branch.'
    case 'checks_failing': return message || 'Some checks are failing.'
    case 'checks_pending': return message || 'Some checks are still running.'
    case 'changes_requested': return message || 'A reviewer requested changes.'
    case 'review_required': return 'An approving review is required.'
    case 'behind_base': return message || 'The branch is behind the base branch.'
    case 'branch_protection': return 'Branch protection may block this.'
    case 'head_moved': return 'The PR changed. Refresh to review again.'
    case 'unknown_mergeable': return 'GitHub has not finished checking this pull request.'
    default: return message || 'This pull request cannot be merged yet.'
  }
}

const METHODS: { value: MergeMethod; label: string }[] = [
  { value: 'merge', label: 'Create a merge commit' },
  { value: 'squash', label: 'Squash and merge' },
  { value: 'rebase', label: 'Rebase and merge' },
]

// Merge dialog: states the exact commit and branches, lists what blocks or warrants a look, and
// merges only on an explicit confirm.
export function MergePanel({ pr, fileCount, client, onClose, onSubmit, onBusyChange }: { pr: PullRequest; client?: ForgeClient; fileCount: number; onClose(): void; onBusyChange(busy: boolean): void; onSubmit(input: MergeInput): Promise<WriteResult> }) {
  const [method, setMethod] = useState<MergeMethod>('merge')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  const local = mergeBlockers(pr)
  // undefined = loading, null = no preflight available (route missing or client without one)
  const [pf, setPf] = useState<MergePreflight | null | undefined>(client?.getMergePreflight ? undefined : null)
  useEffect(() => {
    if (!client?.getMergePreflight) return
    let live = true
    setPf(undefined)
    client.getMergePreflight(pr.ref).then((r) => { if (live) { setPf(r); if (r.defaultMethod) setMethod(r.defaultMethod) } }, () => { if (live) setPf(null) })
    return () => { live = false }
  }, [client, pr.ref.repo.host, pr.ref.repo.owner, pr.ref.repo.name, pr.ref.number, pr.headSha])
  // Decision default (P1): merging uses the operator session. When it is not elevated the dialog says so; Confirm still works and
  // asks for the operator key (operatorFetch), so nothing is blocked client-side.
  const [needsAccess, setNeedsAccess] = useState(false)
  useEffect(() => {
    let live = true
    operatorElevation.status().then((st) => { if (live) setNeedsAccess(!st.elevated) }, () => undefined)
    return () => { live = false }
  }, [])
  const checking = pf === undefined || pf?.state === 'pending'
  const hard = pf ? pf.blockers.filter((b) => b.code !== 'checks_pending').map((b) => blockerSentence(b.code, b.message)) : local.hard
  const soft = pf ? pf.blockers.filter((b) => b.code === 'checks_pending').map((b) => blockerSentence(b.code, b.message)) : local.soft
  const blocked = hard.length > 0 || checking
  const methods = pf && pf.methodsAllowed.length ? METHODS.filter((m) => pf.methodsAllowed.includes(m.value)) : METHODS
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
            {methods.map((m) => (
              <label key={m.value} className={`flex cursor-pointer items-center gap-3 rounded border border-border px-3 py-2.5 text-control ${method === m.value ? 'bg-surface-hover' : 'hover:bg-surface/40'}`}>
                <input type="radio" name="merge-method" value={m.value} checked={method === m.value} onChange={() => setMethod(m.value)} className="accent-primary" />
                <span className="font-medium text-fg">{m.label}</span>
              </label>
            ))}
          </div>
        </fieldset>
        {needsAccess && (
          <p role="status" className="rounded border border-border p-3 text-control text-fg-muted">
            Operator access needed. Merging uses your operator session. Confirm merge will ask for your operator key.
          </p>
        )}
        {checking && <p role="status" className="text-xs text-fg-muted">Checking…</p>}
        {hard.length > 0 && (
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
        {!blocked && !checking && <p className="text-xs text-fg-muted">The forge still checks repository permissions, allowed methods and branch rules. This merges without submitting a review.</p>}
        {error && <p role="alert" className="text-xs text-danger">{error}. Refresh the pull request to check its current state before retrying.</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Merging…' : 'Confirm merge'}</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
