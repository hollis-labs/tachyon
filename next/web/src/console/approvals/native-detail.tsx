import { useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { type ApprovalClient, type ApprovalItem, decisionError, isExpired, isPending } from "./client"

// A native choice is always explicit. No default, undo hold, bulk decision or
// generic acknowledgement is shared with the old card decision bar.
export function NativeGateDetail({ item, client, ready, error, refresh, onChanged }: {
  item: ApprovalItem
  client: ApprovalClient
  ready: boolean
  error: string
  refresh(): Promise<void>
  onChanged(): void
}) {
  const [selected, setSelected] = useState("")
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState("")
  const [ended, setEnded] = useState(false)
  const [refused, setRefused] = useState(false)
  const native = item.native_gate
  const pending = isPending(item) && !isExpired(item) && !ended
  async function submit() {
    if (!native || !selected || !pending || !ready || busy || refused) return
    setBusy(true)
    setFailure("")
    try {
      await client.decideNative(item, selected)
      setEnded(true)
      setSelected("")
      onChanged()
      await refresh()
    } catch (e) {
      setFailure(decisionError(e))
      setSelected("")
      setRefused(true)
      onChanged()
    } finally {
      setBusy(false)
    }
  }
  const chosen = native?.options.find((o) => o.id === item.terminal_outcome?.resolution?.response.decision)
  return <section aria-label="Native gate choice" className="space-y-4 p-4">
    <h2 className="dash-body font-semibold text-fg">{native?.title ?? "Native gate request"}</h2>
    <p className="dash-control text-fg-muted">Once · exact operation · operator review required</p>
    {native && <dl className="space-y-2 dash-support">
      <dt>Adapter</dt><dd>{native.adapter.adapter_id} · {native.adapter.protocol} · {native.adapter.launch_mode}</dd>
      <dt>Enforcement point</dt><dd>{native.enforcement_point}</dd>
      <dt>Operation digest</dt><dd className="break-all font-mono">{native.operation_digest.scheme}:{native.operation_digest.value}</dd>
    </dl>}
    {(error || failure) && <p role="alert" className="dash-control text-danger">{failure || error}</p>}
    {!pending && <p role="status" className="dash-control">{chosen ? `Recorded ${chosen.kind} · ${chosen.id} · Once` : `Request ${item.state}`}. This records a choice; execution is assessed by the host.</p>}
    {pending && native && <fieldset disabled={!ready || busy || refused} className="space-y-2">
      <legend className="dash-control">Choose the exact native option</legend>
      {native.options.map((option) => <label key={option.id} className="flex items-center gap-2 dash-control">
        <input type="radio" name={`native-${item.item_id}`} value={option.id} checked={selected === option.id} onChange={() => setSelected(option.id)} disabled={Date.parse(option.expires_at) <= Date.now()} />
        {option.kind} · {option.id} · Once
      </label>)}
      <Button disabled={!selected || !ready || busy || refused} onClick={() => void submit()}>{busy ? "Recording choice…" : "Record exact choice"}</Button>
    </fieldset>}
    {(!ready || refused || !native) && <Button variant="outline" disabled={busy} onClick={() => { setSelected(""); setFailure(""); setRefused(false); void refresh() }}>Refresh for review</Button>}
  </section>
}
