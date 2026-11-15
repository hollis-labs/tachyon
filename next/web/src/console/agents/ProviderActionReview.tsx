import { useEffect, useRef, useState, type ReactNode } from 'react'
import { newObjectID } from './owned-builder-client'
import { providerActionsClient, type ActionReceipt, type ActionSummary, type ProviderActionsClient } from './provider-actions'
import { Block, Btn } from './ui'

/** All mutations originate in an explicit click. Refresh and polling only read receipts. */
export function ProviderActionReview({ objectID, children, preview, disabled = false, client = providerActionsClient, onLocked }: {
  objectID: string; children?: ReactNode; preview(id: string): Promise<ActionReceipt>; disabled?: boolean
  client?: ProviderActionsClient; onLocked?(locked: boolean): void
}) {
  const [receipt, setReceipt] = useState<ActionReceipt>()
  const [pendingID, setPendingID] = useState<string>()
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState('')
  const [uncertain, setUncertain] = useState(false), [reviewed, setReviewed] = useState(false)
  const [history, setHistory] = useState<ActionSummary[]>(), [next, setNext] = useState<string>()
  const [clock, setClock] = useState(Date.now)
  const running = useRef(false)
  useEffect(() => { setHistory(undefined); setNext(undefined) }, [objectID])
  useEffect(() => { onLocked?.(!!receipt || !!pendingID || busy) }, [receipt, pendingID, busy, onLocked])
  async function perform(action: () => Promise<void>) {
    if (running.current) return
    running.current = true; setBusy(true); setProblem('')
    try { await action() } catch (e) { setProblem(e instanceof Error ? e.message : 'Action not confirmed.') }
    finally { running.current = false; setBusy(false) }
  }
  function remember(row: ActionReceipt) { setReceipt(row); setPendingID(undefined); setUncertain(false); setReviewed(false) }
  useEffect(() => {
    if (receipt?.state !== 'sending') return
    let active = true
    const timer = setInterval(() => {
      setClock(Date.now())
      if (document.visibilityState === 'hidden') return
      void client.get(receipt.id).then(row => {
        if (active) setReceipt(old => old?.id === row.id && old.state === 'sending' && old.attempts <= row.attempts ? row : old)
      }, () => undefined)
    }, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [client, receipt?.id, receipt?.state])
  const loadHistory = (cursor = '') => perform(async () => {
    const page = await client.list(objectID, cursor)
    setHistory(old => cursor ? [...(old ?? []), ...page.items] : page.items); setNext(page.next_before_id)
  })
  const catalog = receipt?.verb.startsWith('catalog.')
  const unconfirmed = receipt && (receipt.state === 'unknown' || receipt.state === 'sending' && (receipt.retry_after ?? Infinity) * 1000 <= clock)
  const canApply = receipt && (receipt.state === 'prepared' || unconfirmed && !catalog)
  return <Block title="Provider action">
    <fieldset disabled={busy || !!receipt || !!pendingID} className="min-w-0 space-y-3">{children}</fieldset>
    {!receipt && !pendingID && <Btn disabled={busy || disabled} onClick={() => void perform(async () => {
      const id = newObjectID(); setPendingID(id); remember(await preview(id))
    })}>Preview provider action</Btn>}
    {pendingID && <div className="space-y-2">
      <p className="dash-body">Preview response not confirmed. Previewing starts no provider action.</p>
      <Btn disabled={busy} onClick={() => void perform(async () => remember(await client.get(pendingID)))}>Check retained preview</Btn>
      <Btn disabled={busy} onClick={() => setPendingID(undefined)}>Discard preview attempt</Btn>
    </div>}
    {receipt && <div className="mt-3 space-y-3">
      <p role="status" className="dash-body">{receipt.verb} · {receipt.state} · saved revision {receipt.object_revision}</p>
      <p className="dash-support text-fg-muted break-all">Target: {receipt.target} · receipt {receipt.id}</p>
      <details open={receipt.state === 'prepared'}><summary className="cursor-pointer dash-support">Complete request</summary><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all dash-support">{JSON.stringify(receipt.payload, null, 2)}</pre></details>
      {receipt.state === 'succeeded' && <details open><summary className="cursor-pointer dash-support">Provider receipt · {receipt.updated_at}</summary><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all dash-support">{JSON.stringify(receipt.result, null, 2)}</pre></details>}
      {receipt.problem && <p className="dash-body text-warning">{receipt.problem}</p>}
      {uncertain && <p className="dash-body text-warning">Response not confirmed. Refresh this receipt before continuing.</p>}
      {catalog && <p className="dash-body text-fg-muted">Creating a provider plan does not install it. Installation requires Tether’s approval of the exact plan and digest. Approval currently happens at Tether; this screen does not collect its passphrase. Catalog requests cannot be blindly replayed.</p>}
      {canApply && <label className="flex items-center gap-2 dash-body"><input type="checkbox" checked={reviewed} disabled={busy || uncertain} onChange={e => setReviewed(e.target.checked)} />I reviewed this exact request and its effects.</label>}
      <div className="flex flex-wrap gap-2">
        <Btn disabled={busy} onClick={() => void perform(async () => remember(await client.get(receipt.id)))}>Refresh receipt</Btn>
        {canApply && <Btn tone="primary" disabled={busy || !reviewed || uncertain} onClick={() => void perform(async () => {
          setUncertain(true); setReviewed(false); remember(await client.apply(receipt, receipt.state !== 'prepared'))
        })}>{unconfirmed ? 'Retry same request' : receipt.verb === 'catalog.plan' ? 'Create Tether plan' : receipt.verb === 'catalog.apply' ? 'Install approved plan' : 'Apply to Tether'}</Btn>}
        {receipt.verb === 'catalog.plan' && receipt.state === 'succeeded' && receipt.result.validation_ok === true && <Btn disabled={busy} onClick={() => void perform(async () => {
          const id = newObjectID(); setPendingID(id); setReceipt(undefined); remember(await client.installation(id, receipt.id))
        })}>Preview installation</Btn>}
        {receipt.verb === 'catalog.apply' && unconfirmed && <Btn disabled={busy} onClick={() => void perform(async () => remember(await client.reconcile(receipt.id)))}>Reconcile installation</Btn>}
        {catalog && unconfirmed && <p className="dash-support text-warning">Check Tether before preparing a replacement. A lost plan response may require provider-side investigation.</p>}
        <Btn disabled={busy} onClick={() => { setReceipt(undefined); setReviewed(false); setUncertain(false); setProblem('') }}>Close receipt</Btn>
      </div>
    </div>}
    {problem && <p role="alert" className="mt-3 dash-body text-warning">{problem}</p>}
    <div className="mt-4 space-y-2">
      <Btn disabled={busy || !!pendingID} onClick={() => void loadHistory()}>{history ? 'Refresh action history' : 'Show action history'}</Btn>
      {history && <ul aria-label="Provider action history">{history.map(row => <li key={row.id}><Btn disabled={busy || !!pendingID} onClick={() => void perform(async () => remember(await client.get(row.id)))}>{row.verb} · {row.state} · {new Date(row.created_at).toLocaleString()}</Btn></li>)}</ul>}
      {next && <Btn disabled={busy} onClick={() => void loadHistory(next)}>More action receipts</Btn>}
    </div>
  </Block>
}
