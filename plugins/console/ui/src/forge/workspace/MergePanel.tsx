import { useRef, useState } from 'react'
import { Btn } from '../../agents/ui'
import type { MergeInput, MergeMethod, PullRequest } from '../types'
import type { WriteResult } from './useWorkspace'

export function MergePanel({ pr, onClose, onSubmit, onBusyChange }: { pr: PullRequest; onClose(): void; onBusyChange(busy: boolean): void; onSubmit(input: MergeInput): Promise<WriteResult> }) {
  const [method, setMethod] = useState<MergeMethod>('merge')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  const blocked = pr.draft || pr.state !== 'open' || !pr.headSha || pr.mergeable === 'conflicting' || pr.mergeable === 'blocked'
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
    <section aria-label="Merge pull request" className="border-b border-fg bg-surface px-4 py-3">
      <p className="text-[13px] text-fg">Merge #{pr.ref.number}: <span className="font-mono">{pr.headBranch}</span> into <span className="font-mono">{pr.baseBranch}</span></p>
      <p className="mt-1 font-mono text-[12px] text-fg-muted">Commit {pr.headSha?.slice(0, 12) ?? 'unavailable'}</p>
      <p className="mt-1 text-[12px] text-fg-muted">GitHub checks repository permissions, allowed merge methods and branch rules. This merges the PR without submitting a review.</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <label className="text-[12px] text-fg-muted">Merge method
          <select value={method} onChange={e => setMethod(e.target.value as MergeMethod)} disabled={busy} className="ml-2 border border-border bg-bg px-2 py-1.5 text-fg">
            <option value="merge">Create a merge commit</option>
            <option value="squash">Squash and merge</option>
            <option value="rebase">Rebase and merge</option>
          </select>
        </label>
        <Btn tone="primary" onClick={() => void submit()} disabled={busy || blocked}>{busy ? 'Merging…' : 'Confirm merge'}</Btn>
        <Btn onClick={onClose} disabled={busy}>Cancel</Btn>
      </div>
      {blocked && <p className="mt-2 text-[12px] text-fg-muted">Resolve the draft, conflicts or branch requirements, then refresh the pull request before merging.</p>}
      {error && <p role="alert" className="mt-2 text-[12px] text-danger">{error}. Refresh the pull request to check its current state before retrying.</p>}
    </section>
  )
}
