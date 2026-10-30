import { useCallback, useEffect, useState, type ReactNode } from "react"
import { Button, Textarea } from "@hollis-labs/design-components"
import { ChevronRight } from "lucide-react"
import { MessageBody } from "../chat/MessageBody"
import {
  isCard,
  isExpired,
  isPending,
  type ApprovalClient,
  type ApprovalItem,
  type ApprovalResponse,
  type CardAnswer,
  type Evidence,
} from "./client"
import { CardData, CardForm, CardReceiptView } from "./card-body"
import { ActionRunPanel } from "./terminal"
import { Countdown, RiskChip, ago, timeLeft } from "./parts"
import { useApprovals } from "./context"

const label = "font-mono dash-caption uppercase tracking-wide text-fg-faint"
function safeHTTPS(raw?: string) {
  try {
    const u = new URL(raw ?? "")
    if (u.protocol === "https:" && !u.username && !u.password) return u.href
  } catch { /* No unsafe links. */ }
  return undefined
}
// Exactly what will run: a bound command, diff and/or affected items, in a mono card.
// Renders nothing when the payload carries none of them.
function RunCard({ request }: { request: ApprovalItem["request_snapshot"] }) {
  const data = request.data ?? {}
  const text = (v: unknown) => (typeof v === "string" && v ? v : undefined)
  const command = text(data.command)
  const diff = text(data.diff)
  const affected = Array.isArray(data.affected) ? data.affected.map(String) : []
  if (!command && !diff && !affected.length) return null
  return (
    <section aria-label="Exactly what will run">
      <h3 className={label}>Exactly what will run</h3>
      <div className="mt-2 space-y-3 rounded border border-border bg-surface/40 p-3 font-mono dash-support text-fg">
        {command && <pre className="whitespace-pre-wrap break-words">$ {command}</pre>}
        {diff && <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words">{diff}</pre>}
        {!!affected.length && (
          <ul aria-label="Affected items" className="space-y-0.5">
            {affected.map((a) => <li key={a}>{a}</li>)}
          </ul>
        )}
      </div>
    </section>
  )
}

const PHRASE = "approve"

function EvidenceView({ evidence }: { evidence: Evidence }) {
  const url = safeHTTPS(typeof evidence.external_url === "string" ? evidence.external_url : undefined)
  return <details className="border-t border-border py-3">
    <summary className="cursor-pointer dash-control text-fg-muted">{evidence.label} · {evidence.type}</summary>
    {evidence.type === "markdown" ? <MessageBody body={evidence.content ?? ""} /> :
      <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words font-mono dash-support text-fg-muted">{evidence.content ?? JSON.stringify(evidence, null, 2)}</pre>}
    {url && <a href={url} target="_blank" rel="noopener noreferrer" className="dash-control text-info">Open evidence</a>}
  </details>
}

// The decision detail: request, exactly what will run, evidence, and the decision bar.
// Shown in the Approvals page (right pane) and in the review dialog.
export function DecisionDetail({
  id,
  initial,
  client,
  onChanged,
  footer,
  initialNote = "",
}: {
  id: string
  initial?: ApprovalItem
  client: ApprovalClient
  onChanged(): void
  footer?: ReactNode
  initialNote?: string
}) {
  const [item, setItem] = useState(initial)
  const [ready, setReady] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [note, setNote] = useState(initialNote)
  const [showNote, setShowNote] = useState(!!initialNote)
  const [typed, setTyped] = useState("")
  const [reply, setReply] = useState("")
  const [choice, setChoice] = useState<ApprovalResponse["decision"]>()
  const refresh = useCallback(async () => {
    setReady(false)
    setChoice(undefined)
    setError("")
    try {
      setItem(await client.present(id))
      setReady(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Unable to review request")
    }
  }, [client, id])
  useEffect(() => {
    void refresh()
  }, [refresh])
  async function decide(submitted?: CardAnswer[], direct?: ApprovalResponse["decision"]) {
    const decision = submitted ? "submitted" : (direct ?? choice)
    if (!item || !decision || busy || !ready) return
    setBusy(true)
    setError("")
    try {
      const outcome = await client.decide(item, {
        kind: item.request_snapshot.kind,
        decision,
        ...(submitted ? { answers: submitted } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        ...(reply.trim() && item.request_snapshot.kind === "attention"
          ? { reply: reply.trim() }
          : {}),
      })
      setItem({ ...item, state: outcome.state, terminal_outcome: outcome })
      setChoice(undefined)
      onChanged()
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Decision not confirmed. Refresh to check the outcome.",
      )
      setReady(false)
      setChoice(undefined)
      onChanged()
    } finally {
      setBusy(false)
    }
  }
  const queue = useApprovals()
  const isHeld = !!queue?.held.some((h) => h.itemId === id)
  // Low/medium decisions are held for undo in the queue provider; high-risk and the
  // no-provider case decide at once.
  function decideOrHold(decision: "approved" | "denied") {
    if (!item || !ready || busy) return
    // Only an approval of a low/medium item is held; a denial is safe and immediate.
    if (!queue || item.request_snapshot.class === "high" || decision === "denied")
      return void decide(undefined, decision)
    queue.hold(item, {
      kind: item.request_snapshot.kind,
      decision,
      ...(note.trim() ? { note: note.trim() } : {}),
    })
  }
  const request = item?.request_snapshot
  // High-risk requests are approvable here, gated by a typed phrase (the server still enforces).
  const needsPhrase = request?.class === "high"
  const card = !!request && isCard(request)
  const resolution = item?.terminal_outcome?.resolution
  const canDecide =
    item && isPending(item) && !isExpired(item) && ready && !busy && !isHeld && request?.approvable !== false
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <header>
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="min-w-0 font-sans normal-case tracking-normal dash-body font-semibold text-fg">{request?.title ?? "Review request"}</h2>
            {request && <RiskChip risk={request.class} />}
          </div>
          <p className="mt-1 font-mono dash-support text-fg-muted">
            {request?.source.agent_label ?? request?.source.agent_id} ·{" "}
            {request?.source.application_label ?? request?.source.application_id}
            {item && <> · {item.state}{isExpired(item) && isPending(item) ? " · expired" : ""}</>}
            {request?.expires_at && item && isPending(item) && <> · <Countdown expiresAt={request.expires_at} /></>}
          </p>
        </header>
        {error && (
          <p
            role="alert"
            className="rounded border border-danger/30 bg-danger-muted/20 p-3 dash-control text-danger"
          >
            {error}
          </p>
        )}
        {!ready && !error && (
          <p role="status" className="dash-control text-fg-faint">
            Checking the current request…
          </p>
        )}
        {request && (
          <>
            <section>
              <h3 className={label}>Request</h3>
              {request.request && <MessageBody body={request.request} />}
              {request.summary && request.summary !== request.request && (
                <MessageBody body={request.summary} />
              )}
              {card && <CardData request={request} />}
            </section>
            <RunCard request={request} />
            {request.details_markdown && (
              <section>
                <h3 className={label}>Details</h3>
                <MessageBody body={request.details_markdown} />
              </section>
            )}
            {request.recommendation && (
              <section>
                <h3 className={label}>Recommendation</h3>
                <MessageBody body={request.recommendation} />
              </section>
            )}
            {request.impact && (
              <div className="grid gap-4 border-y border-border py-4 sm:grid-cols-2">
                <section>
                  <h3 className={`${label} text-success`}>If approved</h3>
                  <MessageBody body={request.impact.approve} />
                </section>
                <section>
                  <h3 className={`${label} text-danger`}>If denied</h3>
                  <MessageBody body={request.impact.deny} />
                </section>
              </div>
            )}
            {item?.action_run && (
              <ActionRunPanel
                initial={item.action_run}
                approved={resolution?.response.decision === "approved"}
              />
            )}
            <dl className="grid grid-cols-2 gap-4 dash-support text-fg-muted">
              <div>
                <dt className={label}>Requested</dt>
                <dd title={new Date(item!.enqueued_at).toLocaleString()}>{ago(item!.enqueued_at)}</dd>
              </div>
              <div>
                <dt className={label}>Expires</dt>
                <dd>
                  {request.expires_at
                    ? <span title={new Date(request.expires_at).toLocaleString()}>{timeLeft(request.expires_at) ?? "No expiry"}</span>
                    : "No expiry"}
                </dd>
              </div>
            </dl>
            {request.correlations && (
              <details>
                <summary className="cursor-pointer dash-control text-fg-muted">
                  Related work
                </summary>
                <MessageBody
                  body={JSON.stringify(request.correlations, null, 2)}
                />
              </details>
            )}
            {!!request.evidence?.length && (
              <section>
                <h3 className={label}>Evidence</h3>
                {request.evidence.map((e, i) => (
                  <EvidenceView
                    key={i}
                    evidence={e}
                  />
                ))}
              </section>
            )}
            {item?.card_receipt && <CardReceiptView receipt={item.card_receipt} />}
          </>
        )}
      </div>
      {/* Decision bar: fixed at the bottom of the detail. */}
      <div role="toolbar" aria-label="Decision" className="max-h-[40vh] space-y-3 overflow-y-auto border-t border-border bg-bg p-4">
        {request && (<>
            {resolution ? (
              <section className="rounded border border-border bg-surface/40 p-4">
                <h3 className={`${label} text-info`}>
                  {resolution.response.decision}
                </h3>
                <p className="mt-2 dash-control text-fg-muted">
                  {new Date(resolution.resolved_at).toLocaleString()} ·{" "}
                  {resolution.participant.principal_ref}
                </p>
                {resolution.response.note && (
                  <MessageBody body={resolution.response.note} />
                )}
                {resolution.response.reply && (
                  <MessageBody body={resolution.response.reply} />
                )}
                {!!resolution.response.answers?.length && (
                  <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono dash-support text-fg-muted">{JSON.stringify(resolution.response.answers, null, 2)}</pre>
                )}
              </section>
            ) : item?.terminal_outcome ? (
              <MessageBody
                body={
                  item.terminal_outcome.reason ??
                  item.terminal_outcome.message ??
                  `Request ${item.state}`
                }
              />
            ) : request.approvable === false ? (
              <section className="rounded border border-border p-4">
                <p className="dash-control text-fg-muted">This request cannot be approved here. Complete the decision in the provider console.</p>
                {ready && safeHTTPS(request.handoff_url) && <a href={safeHTTPS(request.handoff_url)} target="_blank" rel="noopener noreferrer" className="dash-control text-info">Open in {request.source.application_id === "cerberus" ? "Cerberus" : "provider console"}</a>}
              </section>
            ) : (
              <>
                {!showNote ? (
                  <button type="button" aria-expanded={false} className="inline-flex items-center gap-1 dash-control text-fg-muted underline underline-offset-2 hover:text-fg" onClick={() => setShowNote(true)}>
                    <ChevronRight className="h-3 w-3" aria-hidden /> Add note
                  </button>
                ) : (
                <label className="block dash-control text-fg-muted">
                  Decision note (optional)
                  <Textarea
                    aria-label="Decision note"
                    disabled={busy}
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    maxLength={8000}
                    rows={3}
                    className="mt-2 w-full dash-control"
                  />
                </label>
                )}
                {request.kind === "attention" && (
                  <label className="block dash-control text-fg-muted">
                    Reply (optional)
                    <Textarea
                      aria-label="Approval reply"
                      disabled={busy}
                      value={reply}
                      onChange={(e) => setReply(e.target.value)}
                      maxLength={8000}
                      rows={3}
                      className="mt-2 w-full dash-control"
                    />
                  </label>
                )}
                {request.kind === "card.info" ? (
                  <p className="dash-control text-fg-muted">This card is for information; there is nothing to answer.</p>
                ) : request.kind === "card.question" || request.kind === "card.form" ? (
                  <CardForm request={request} busy={busy} disabled={!canDecide} onSubmit={(answers) => void decide(answers)} />
                ) : choice ? (
                  <section className="rounded border border-border bg-surface/40 p-3">
                    <p className="dash-control text-fg-muted">
                      Confirm{" "}
                      {choice === "approved"
                        ? "approval"
                        : choice === "denied"
                          ? "denial"
                          : "acknowledgement"}{" "}
                      of this request?
                    </p>
                    {needsPhrase && choice === "approved" && (
                      <label className="mt-3 block dash-control text-fg-muted">
                        Type <span className="font-mono text-fg">{PHRASE}</span> to approve this high-risk request
                        <input
                          aria-label="Confirmation phrase"
                          autoComplete="off"
                          value={typed}
                          onChange={(e) => setTyped(e.target.value)}
                          className="mt-2 w-full border border-border bg-bg p-2 font-mono dash-control"
                        />
                      </label>
                    )}
                    <div className="mt-3 flex gap-2">
                      <Button
                        disabled={!canDecide || (needsPhrase && choice === "approved" && typed.trim().toLowerCase() !== PHRASE)}
                        onClick={() => void decide()}
                      >
                        {busy ? "Submitting…" : "Confirm decision"}
                      </Button>
                      <Button variant="outline"
                        disabled={busy}
                        onClick={() => setChoice(undefined)}
                      >
                        Back
                      </Button>
                    </div>
                  </section>
                ) : (
                  <div className="flex gap-2">
                    {request.kind === "approval" || request.kind === "card.approval" ? (
                      <>
                        <Button
                          className="flex-1 sm:flex-none"
                          disabled={!canDecide}
                          onClick={() => (needsPhrase ? setChoice("approved") : decideOrHold("approved"))}
                        >
                          {busy || isHeld ? "Approving…" : item?.action_run ? "Approve and run" : "Approve"}
                        </Button>
                        <Button variant="outline"
                          className="flex-1 sm:flex-none"
                          disabled={!canDecide}
                          onClick={() => decideOrHold("denied")}
                        >
                          Deny
                        </Button>
                      </>
                    ) : (
                      <Button
                        disabled={!canDecide}
                        onClick={() => void decide(undefined, "acknowledged")}
                      >
                        Acknowledge
                      </Button>
                    )}
                  </div>
                )}
                <p className="dash-support text-fg-faint">
                  Records your decision for the requesting application.
                  Execution and credential permissions are checked by that
                  application.
                </p>
              </>
            )}
        </>)}
        {(error || footer) && (
          <div className="flex justify-between gap-2">
            {error ? <Button variant="outline" disabled={busy} onClick={() => void refresh()}>Review again</Button> : <span />}
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}
