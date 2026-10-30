import { Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogTitle } from '@hollis-labs/design-components'
import { useEffect, useState } from 'react'
import { builderApi, type Change, errorMessage } from './api'
import { isOpen, isWaiting, minutesAndSeconds } from './changes'
import { useBuilderStream } from './stream'

const CHECK_WORD = { ok: 'Passed', warn: 'Warning', fail: 'Failed' } as const

/** Follow an open change: the stream says when it moves; a slow poll is the safety net (fast when the stream is down). */
function useChange(initial: Change): [Change, (c: Change) => void] {
  const [change, setChange] = useState(initial)
  const refetch = () => builderApi.change(change.change_id).then(setChange, () => undefined)
  const live = useBuilderStream((e) => {
    if (e.type === 'change' && e.change_id === change.change_id) void refetch()
  })
  // biome-ignore lint/correctness/useExhaustiveDependencies: refetch is rebuilt each render from change_id
  useEffect(() => {
    if (!isWaiting(change.state)) return
    const t = setInterval(
      () => {
        if (!document.hidden) void refetch()
      },
      live ? 30000 : 3000,
    )
    return () => clearInterval(t)
  }, [change.change_id, change.state, live])
  return [change, setChange]
}

export function Diff({ text }: { text: string }) {
  return (
    <pre aria-label="Changes" className="max-h-56 overflow-auto border border-border bg-surface p-2 font-mono dash-support text-fg">
      {text.split('\n').map((l, i) => (
        <span key={i} className={`block ${l.startsWith('+') ? 'bg-success/10 text-fg' : l.startsWith('-') ? 'bg-danger/10 text-fg' : 'text-fg-faint'}`}>
          {l.startsWith('+') ? '+ ' : l.startsWith('-') ? '− ' : '  '}
          {l.slice(1) || ' '}
        </span>
      ))}
    </pre>
  )
}

function Countdown({ expiresAt }: { expiresAt: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  const left = expiresAt - now
  return <span>{left > 0 ? `Expires in ${minutesAndSeconds(left)}` : 'Expired'}</span>
}

/** Review a computed change, submit it, and follow it until it is applied or ends. */
export function ReviewDialog({ initial, onClose, onApplied, onReload }: { initial: Change; onClose: () => void; onApplied: () => void; onReload: () => void }) {
  const [change, setChange] = useChange(initial)
  const [ack, setAck] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const privileged = (change.privileged ?? []).length > 0
  const failChecks = (change.checks ?? []).filter((c) => c.status === 'fail')

  useEffect(() => {
    if (change.state === 'applied') onApplied()
  }, [change.state, onApplied])

  const run = async (fn: () => Promise<Change>) => {
    setBusy(true)
    setError('')
    try {
      setChange(await fn())
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const done = !isOpen(change.state)

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogTitle>{change.state === 'draft' ? 'Review changes' : 'Change status'}</DialogTitle>
        <DialogDescription className="sr-only">Review the computed change and submit it for approval on the host.</DialogDescription>
        {change.diff && <Diff text={change.diff} />}
        {(change.checks ?? []).length > 0 && (
          <ul aria-label="Checks" className="space-y-1">
            {change.checks?.map((c) => (
              <li key={c.name + (c.entity ?? '')} className="dash-support text-fg-secondary">
                <span className={c.status === 'fail' ? 'text-danger' : ''}>{CHECK_WORD[c.status]}</span> {c.message}
                {c.remedy ? ` ${c.remedy}` : ''}
              </li>
            ))}
          </ul>
        )}
        {privileged && <p className="dash-support text-fg-secondary">Privileged: {change.privileged?.map((p) => p.field || p.path).join(', ')}</p>}
        <div className="border border-border p-3 dash-support text-fg-secondary">
          <p className="font-medium text-fg">Effect</p>
          <p>{change.effect?.activation || 'Activation is reported after approval.'}</p>
          {(change.effect?.running_sessions ?? []).length > 0 && <p>{change.effect?.running_sessions?.length} running session(s) keep their config until restarted.</p>}
          {(change.effect?.restart_required ?? []).length > 0 && <p>Restart required: {change.effect?.restart_required?.join(', ')}</p>}
          <p>Needs approval on the host.</p>
        </div>

        {change.state === 'awaiting_approval' && change.plan && (
          <div className="space-y-2 border border-border p-3" role="status">
            <p className="dash-body text-fg">Awaiting approval. Run this on the host:</p>
            <p className="dash-support text-fg-muted">Nothing changes until it is approved on the host.</p>
            <code className="block break-all bg-surface p-2 font-mono dash-support text-fg">{change.plan.approve_command}</code>
            <div className="flex items-center gap-3 dash-support text-fg-muted">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  void navigator.clipboard?.writeText(change.plan?.approve_command ?? '')
                  setCopied(true)
                  setTimeout(() => setCopied(false), 1500)
                }}
              >
                {copied ? 'Copied' : 'Copy command'}
              </Button>
              <Countdown expiresAt={change.plan.expires_at} />
              {change.plan.expires_at <= Date.now() && (
                <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => run(() => builderApi.rearm(change.change_id))}>
                  Re-arm
                </Button>
              )}
            </div>
          </div>
        )}
        {(change.state === 'approved' || change.state === 'applying') && (
          <p role="status" className="dash-body text-fg">
            Applying…
          </p>
        )}
        {change.state === 'applied' && (
          <p role="status" className="dash-body text-fg">
            Saved. {change.result?.activation}
            {change.result?.reload_error ? ` Reload reported: ${change.result.reload_error}` : ''}
          </p>
        )}
        {change.state === 'failed' && (
          <p role="alert" className="dash-body text-danger">
            Failed at {change.failure?.step}: {change.failure?.message}. Your draft is kept.
          </p>
        )}
        {change.state === 'expired' && (
          <p role="alert" className="dash-body text-danger">
            The approval window ended. Your draft is kept.
          </p>
        )}
        {change.state === 'rolled_back' && (
          <p role="alert" className="dash-body text-danger">
            The change was rolled back.
          </p>
        )}
        {change.state === 'cancelled' && (
          <p role="status" className="dash-body text-fg">
            Change cancelled.
          </p>
        )}
        {change.state === 'stale' && (
          <div role="alert" className="space-y-2 border border-border p-3">
            <p className="dash-body text-fg">Changed elsewhere: {change.stale?.changed_paths.join(', ')}.</p>
            {change.stale?.theirs_diff && <Diff text={change.stale.theirs_diff} />}
            <Button type="button" variant="outline" size="sm" onClick={onReload}>
              Reload theirs
            </Button>
          </div>
        )}
        {error && (
          <p role="alert" className="dash-support text-danger">
            {error}
          </p>
        )}

        <div className="flex flex-wrap items-center justify-end gap-2">
          {change.state === 'draft' && privileged && (
            <label className="mr-auto flex items-center gap-2 dash-support text-fg-secondary">
              <Checkbox checked={ack} onCheckedChange={(v) => setAck(v === true)} /> I understand these fields are privileged
            </label>
          )}
          {(change.state === 'draft' || change.state === 'awaiting_approval' || change.state === 'stale') && (
            <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => (change.state === 'draft' ? onClose() : run(() => builderApi.cancel(change.change_id)))}>
              {change.state === 'draft' ? 'Keep editing' : 'Cancel change'}
            </Button>
          )}
          {change.state === 'draft' && (
            <Button type="button" variant="default" size="sm" disabled={busy || failChecks.length > 0 || (privileged && !ack)} onClick={() => run(() => builderApi.submit(change.change_id, ack))}>
              {busy ? 'Submitting' : 'Submit for approval'}
            </Button>
          )}
          {done && (
            <Button type="button" variant="outline" size="sm" onClick={onClose}>
              Close
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
