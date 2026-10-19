import { useEffect, useState } from 'react'
import { Field, inputCls } from './AgentEditor'
import { newObjectID } from './owned-builder-client'
import type { OwnedTeam } from './owned-teams'
import { createTeamActionsClient, type FormationReceipt, type FormationSummary, type TeamActionsClient } from './team-actions'
import { Block, Btn } from './ui'

const defaultClient = createTeamActionsClient()
const labels = { prepared: 'Ready for review', sending: 'Request pending', unknown: 'Outcome unknown', succeeded: 'Formation confirmed' }

/** Receipts describe provider responses at a point in time, never live health. */
export function TeamFormation({ team, client = defaultClient }: { team: OwnedTeam; client?: TeamActionsClient }) {
  const [mission, setMission] = useState('')
  const [receipt, setReceipt] = useState<FormationReceipt | null>(null)
  const [pendingID, setPendingID] = useState<string | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState<FormationSummary[] | null>(null)
  const [next, setNext] = useState<string | undefined>()
  const [clock, setClock] = useState(() => Date.now())
  const [selectedForReview, setSelectedForReview] = useState(false)
  const remember = (row: FormationReceipt) => {
    setReceipt(row); setPendingID(null); setUncertain(false)
    setHistory(items => items ? [row, ...items.filter(item => item.id !== row.id)] : items)
  }
  async function perform(work: () => Promise<void>) {
    if (busy) return
    setBusy(true); setError(null)
    try { await work() } catch (e) { setError(e instanceof Error ? e.message : 'Action could not be confirmed.') }
    finally { setBusy(false) }
  }
  const refresh = () => perform(async () => {
    const id = pendingID ?? receipt?.id
    if (id) remember(await client.get(id))
  })
  // Poll only retained pending receipts, never apply or retry a mutation.
  useEffect(() => {
    if (receipt?.state !== 'sending') return
    let active = true
    const timer = setInterval(() => {
      setClock(Date.now())
      if (document.visibilityState === 'hidden') return
      void client.get(receipt.id).then(row => { if (active) { setReceipt(current => current?.id === row.id && current.state === 'sending' && current.attempts <= row.attempts ? row : current) } }).catch(() => {})
    }, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [client, receipt?.id, receipt?.state])
  const loadHistory = (before = '') => perform(async () => {
    const page = await client.list(team.id, before)
    setHistory(items => before ? [...(items ?? []), ...page.items] : page.items); setNext(page.next_before_id)
  })
  const canRetry = receipt && (receipt.state === 'unknown' || receipt.state === 'sending' && (receipt.retry_after ?? Infinity) * 1000 <= clock)
  const stale = receipt?.state === 'prepared' && (receipt.object_revision !== team.revision || !!team.deleted_at)
  return <section aria-label="Team formation">
    <Block title="Form a team in Tether">
      <p className="dash-body text-fg-muted">Uses saved revision {team.revision} of {team.name}. Save draft changes before preparing a formation. Recover any pending request from formation history before creating another run. Tether checks the pinned definitions, caller permissions and current launch policy when you apply.</p>
      {!receipt && <>
        <Field label="Formation mission" htmlFor={'formation-mission-' + team.id}>
          <textarea id={'formation-mission-' + team.id} className={inputCls} value={mission} maxLength={8192} disabled={busy || !!pendingID} onChange={e => setMission(e.target.value)} />
        </Field>
        <Btn disabled={busy || !!pendingID || !!team.deleted_at || !mission.trim()} onClick={() => void perform(async () => {
          const id = newObjectID(); setPendingID(id); setSelectedForReview(false)
          remember(await client.preview(id, team.id, team.revision, mission))
        })}>Preview formation</Btn>
      </>}
      {pendingID && <div className="mt-3 space-y-2">
        <p className="dash-body text-fg-muted">Preview response was not confirmed. No formation is applied by creating a preview.</p>
        <Btn disabled={busy} onClick={() => void refresh()}>Check retained preview</Btn>
        <Btn disabled={busy} onClick={() => { setPendingID(null); setError(null) }}>Discard preview attempt</Btn>
      </div>}
      {receipt && <div className="mt-3 space-y-3">
        <p role="status" className="dash-body">{labels[receipt.state]} · saved revision {receipt.object_revision}</p>
        <p className="dash-support text-fg-muted">Target: {receipt.target} · team {receipt.payload.team.name} v{receipt.payload.team.version}</p>
        <p className="dash-support text-fg-muted break-all">Formation definition: {receipt.payload.team.id}</p>
        <p className="dash-body whitespace-pre-wrap">Mission: {receipt.payload.team.slots[0]?.workspace?.mission}</p>
        <ul className="space-y-1 dash-support">
          {receipt.payload.team.slots.map(slot => <li key={slot.name}>{slot.name}: {slot.min} initial agent · {slot.definition_ref.id}@{slot.definition_ref.revision}</li>)}
        </ul>
        <p className="dash-support text-fg-muted">Tether assigns its authenticated caller as owner. Required worker seats may start agent processes.</p>
        <details><summary className="cursor-pointer dash-support">Review complete provider request</summary><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all dash-support">{JSON.stringify(receipt.payload, null, 2)}</pre></details>
        {receipt.problem && <p className="dash-body text-warning">{receipt.problem}</p>}
        {stale && <p className="dash-body text-warning">This preview uses an older or retired saved definition. Prepare a new preview.</p>}
        {uncertain && <p className="dash-body text-warning">The response was lost or refused. Refresh this receipt before continuing; it may already have been sent.</p>}
        {receipt.result.run && <p className="dash-body">Run {receipt.result.run.id} · reported status {receipt.result.run.status}. Receipt recorded {new Date(receipt.updated_at).toLocaleString()}; current runtime health is not checked here.</p>}
        {(receipt.state === 'prepared' || canRetry) && !uncertain && !stale && <label className="flex items-center gap-2 dash-body"><input type="checkbox" checked={selectedForReview} disabled={busy} onChange={e => setSelectedForReview(e.target.checked)} />I reviewed this request and its possible agent launches.</label>}
        <div className="flex flex-wrap gap-2">
          <Btn disabled={busy} onClick={() => void refresh()}>Refresh receipt</Btn>
          {(receipt.state === 'prepared' || canRetry) && <Btn tone="primary" disabled={busy || uncertain || stale || !selectedForReview} onClick={() => void perform(async () => {
            setSelectedForReview(false); setUncertain(true)
            remember(await client.apply(receipt, receipt.state !== 'prepared'))
          })}>{canRetry ? 'Retry same formation request' : 'Form team in Tether'}</Btn>}
          <Btn disabled={busy} onClick={() => { setReceipt(null); setSelectedForReview(false); setError(null); setUncertain(false) }}>Close receipt</Btn>
        </div>
      </div>}
      {error && <p role="alert" className="mt-3 dash-body text-warning">{error}</p>}
      <div className="mt-4 space-y-2">
        <Btn disabled={busy} onClick={() => void loadHistory()}>{history ? 'Refresh formation history' : 'Show formation history'}</Btn>
        {history && <ul aria-label="Formation history" className="space-y-2">{history.map(row => <li key={row.id}><Btn disabled={busy} onClick={() => void perform(async () => { remember(await client.get(row.id)); setSelectedForReview(false) })}>{labels[row.state]} · revision {row.object_revision} · {new Date(row.created_at).toLocaleString()}</Btn></li>)}</ul>}
        {next && <Btn disabled={busy} onClick={() => void loadHistory(next)}>Load more formation receipts</Btn>}
      </div>
    </Block>
  </section>
}
