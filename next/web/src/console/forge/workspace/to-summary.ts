import type { PendingPrSummary, PullRequest } from '../types'

/** The inbox's view of a PR, for dialogs that take a summary (assign) when opened from the workspace. */
export function toSummary(pr: PullRequest): PendingPrSummary {
  const failed = pr.checks.filter((c) => c.status === 'completed' && !['success', 'skipped', 'neutral'].includes(c.conclusion ?? '')).length
  const pending = pr.checks.filter((c) => c.status !== 'completed').length
  const passed = pr.checks.length - failed - pending
  const changes = pr.reviews.filter((r) => r.state === 'changes_requested').length
  const reasons: PendingPrSummary['reasons'] = []
  if (failed) reasons.push('checks_failed')
  if (changes) reasons.push('changes_requested')
  if (pr.mergeable === 'conflicting') reasons.push('conflicting')
  if (pr.assignees.length === 0) reasons.push('unassigned')
  return {
    ref: pr.ref,
    title: pr.title,
    author: pr.author,
    assignees: pr.assignees,
    state: pr.state,
    reasons,
    checksSummary: { passed, failed, pending },
    reviewSummary: { approved: pr.reviews.filter((r) => r.state === 'approved').length, changesRequested: changes },
    mergeable: pr.mergeable,
    updatedAt: pr.updatedAt,
    additions: pr.additions,
    deletions: pr.deletions,
    draft: pr.draft,
    changedFiles: pr.changedFiles,
    url: pr.url,
  }
}
