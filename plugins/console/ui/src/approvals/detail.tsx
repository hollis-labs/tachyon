import { useCallback, useEffect, useState, type ReactNode } from "react"
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Textarea } from "@hollis-labs/design-components"
import { FileText, GitCompare, Paperclip } from "lucide-react"
import { Chip } from "../components/chip"
import { Keycap } from "../components/keycap"
import { MessageBody } from "../chat/MessageBody"
import {
  isCard,
  isExpired,
  isPending,
  type ApprovalClient,
  type ApprovalItem,
  type ApprovalResponse,
  type CardAnswer,
  handoffItems,
  isHandoffError,
  type Evidence,
} from "./client"
import { CardData, CardForm, CardReceiptView } from "./card-body"
import { ActionRunPanel } from "./terminal"
import { Countdown, RiskChip, ago, timeLeft } from "./parts"
import { AuditTrail } from "./audit"
import { useApprovals } from "./context"

const label = "font-mono dash-caption uppercase tracking-label text-fg-faint"
function safeHTTPS(raw?: string) {
  try {
    const u = new URL(raw ?? "")
    if (u.protocol === "https:" && !u.username && !u.password) return u.href
  } catch { /* No unsafe links. */ }
  return undefined
}
// Exactly what will run: a bound command, diff and/or affected items, in a mono card.
// Renders nothing when the payload carries none of them. A registered action's own run card
// (machine, state, terminal) replaces the plain command.
const AFFECTED_SHOWN = 4
function RunCard({ request, item, run }: { request: ApprovalItem["request_snapshot"]; item?: ApprovalItem; run?: ReactNode }) {
  const data = request.data ?? {}
  const text = (v: unknown) => (typeof v === "string" && v ? v : undefined)
  const command = run ? undefined : text(data.command)
  const diff = text(data.diff)
  // The server's affected_items (label and note) win over the free-form list a producer put in data.
  const served = item?.affected_items ?? []
  const affected = served.length ? served.map((a) => (a.note ? `${a.label} (${a.note})` : a.label)) : Array.isArray(data.affected) ? data.affected.map(String) : []
  const affectedTotal = Math.max(item?.affected_total ?? 0, affected.length)
  if (!command && !diff && !affected.length && !run) return null
  return (
    <section aria-label="Exactly what will run">
      <h3 className={label}>Exactly what will run</h3>
      {run && <div className="mt-2">{run}</div>}
      {(command || diff || !!affected.length) && (
        <div className="mt-2 space-y-3 rounded border border-border bg-surface/40 p-3 font-mono dash-support text-fg">
          {command && <pre className="whitespace-pre-wrap break-words">$ {command}</pre>}
          {diff && <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words">{diff}</pre>}
          {!!affected.length && (
            <div>
              <h4 className={label}>Affected items</h4>
              <ul aria-label="Affected items" className="mt-1.5 space-y-0.5">
                {affected.slice(0, AFFECTED_SHOWN).map((a) => <li key={a}>{a}</li>)}
              </ul>
              {affectedTotal > Math.min(AFFECTED_SHOWN, affected.length) && <p className="mt-1 text-fg-muted">+ {affectedTotal - Math.min(AFFECTED_SHOWN, affected.length)} more</p>}
            </div>
          )}
        </div>
      )}
    </section>
  )
}

const PHRASE = "approve"
// Presets per G5: 1 hour is the first and default choice.
const DEFER_PRESETS = [
  { label: "1 hour", seconds: 3600 },
  { label: "4 hours", seconds: 4 * 3600 },
  { label: "Tomorrow", seconds: 24 * 3600 },
]

const EVIDENCE_ICON = { markdown: FileText, diff: GitCompare } as const
function EvidenceView({ evidence }: { evidence: Evidence }) {
  const url = safeHTTPS(typeof evidence.external_url === "string" ? evidence.external_url : undefined)
  const Icon = EVIDENCE_ICON[evidence.type as keyof typeof EVIDENCE_ICON] ?? Paperclip
  return <details className="group border-b border-border last:border-b-0">
    <summary className="flex cursor-pointer list-none items-center gap-3 px-3 py-2 dash-control">
      <Icon aria-hidden className="size-3.5 shrink-0 text-fg-muted" />
      <span className="font-medium text-fg">{evidence.label}</span>
      <span className="min-w-0 flex-1 truncate text-fg-muted">{evidence.type}</span>
      <span className="dash-label text-fg-muted group-open:text-fg">View</span>
    </summary>
    <div className="border-t border-border px-3 py-2">
      {evidence.type === "markdown" ? <MessageBody body={evidence.content ?? ""} /> :
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words font-mono dash-support text-fg-muted">{evidence.content ?? JSON.stringify(evidence, null, 2)}</pre>}
      {url && <a href={url} target="_blank" rel="noopener noreferrer" className="dash-control text-info">Open evidence</a>}
    </div>
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
  const [handoff, setHandoff] = useState(handoffItems.has(id))
  useEffect(() => setHandoff(handoffItems.has(id)), [id])
  const [reply, setReply] = useState("")
  const refresh = useCallback(async () => {
    setReady(false)
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
  async function defer(seconds: number) {
    if (!item || busy) return
    setBusy(true)
    setError("")
    try {
      const r = await client.defer(item, seconds)
      setItem({ ...item, deferred_until: r.deferred_until, revision: r.revision })
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not snooze. Refresh and try again.")
    } finally {
      setBusy(false)
    }
  }
  async function bringBack() {
    if (!item || busy) return
    setBusy(true)
    setError("")
    try {
      const r = await client.undefer(item.item_id)
      setItem({ ...item, deferred_until: null, revision: r.revision })
      onChanged()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not bring this back. Refresh and try again.")
    } finally {
      setBusy(false)
    }
  }
  async function decide(submitted?: CardAnswer[], direct?: ApprovalResponse["decision"], alternativeId?: string) {
    const decision = submitted ? "submitted" : direct
    if (!item || !decision || busy || !ready) return
    setBusy(true)
    setError("")
    try {
      const outcome = await client.decide(item, {
        kind: item.request_snapshot.kind,
        decision,
        ...(submitted ? { answers: submitted } : {}),
        ...(note.trim() ? { note: note.trim() } : {}),
        ...(decision === "approved" && item.confirm?.phrase && !alternativeId ? { confirmation: typed.trim() } : {}),
        ...(alternativeId ? { alternative_id: alternativeId } : {}),
        ...(reply.trim() && item.request_snapshot.kind === "attention"
          ? { reply: reply.trim() }
          : {}),
      })
      setItem({ ...item, state: outcome.state, terminal_outcome: outcome })
        onChanged()
    } catch (e) {
      if (isHandoffError(e)) {
        // Not decidable in the browser: show the handoff state, not the raw error.
        handoffItems.add(item.item_id)
        setHandoff(true)
            setTyped("")
        return
      }
      setError(
        e instanceof Error
          ? e.message
          : "Decision not confirmed. Refresh to check the outcome.",
      )
      setReady(false)
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
    if (!queue || item.request_snapshot.class === "high" || item.confirm || decision === "denied")
      return void decide(undefined, decision)
    queue.hold(item, {
      kind: item.request_snapshot.kind,
      decision,
      ...(note.trim() ? { note: note.trim() } : {}),
    })
  }
  const request = item?.request_snapshot
  // High-risk requests are approvable here, gated by a typed phrase (the server still enforces).
  // The server derives the phrase (item.confirm); the client only guesses for a producer that sends none.
  const needsPhrase = !!item?.confirm?.phrase || request?.class === "high"
  const affectedCount = Array.isArray(request?.data?.affected) ? request.data.affected.length : 0
  const phrase = item?.confirm?.phrase ?? (affectedCount ? `DELETE ${affectedCount}` : PHRASE)
  const phraseOk = item?.confirm?.phrase ? typed.trim() === phrase : typed.trim().toLowerCase() === phrase.toLowerCase()
  // A denial needs a reason when the server says so.
  const denyNeedsReason = !!item?.reason_required && !note.trim()
  const card = !!request && isCard(request)
  const resolution = item?.terminal_outcome?.resolution
  const canDecide =
    item && isPending(item) && !isExpired(item) && ready && !busy && !isHeld && request?.approvable !== false && !handoff
  const approve = () => (needsPhrase ? phraseOk && void decide(undefined, "approved") : decideOrHold("approved"))
  // A and D act only while focus is on a button in the decision bar, never in a field. A high-risk
  // approval still needs the typed phrase.
  function onBarKey(e: React.KeyboardEvent) {
    if (e.metaKey || e.ctrlKey || e.altKey || !(e.target instanceof HTMLButtonElement)) return
    const k = e.key.toLowerCase()
    if (k !== "a" && k !== "d") return
    if (!request || !(request.kind === "approval" || request.kind === "card.approval") || !canDecide) return
    e.preventDefault()
    if (k === "d") decideOrHold("denied")
    else approve()
  }
  const agent = request?.source.agent_label ?? request?.source.agent_id
  const bullets = (b: string) => <MessageBody body={b} />
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <header className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="font-sans normal-case tracking-normal dash-body font-semibold text-fg">{request?.title ?? "Review request"}</h2>
            <p className="mt-1 dash-support text-fg-muted">
              {agent} asked {item ? ago(item.enqueued_at) : ""} · session {request?.source.agent_id}
              {item && isExpired(item) && isPending(item) ? " · expired" : ""}
              {request?.expires_at && item && isPending(item) && <> · expires in <Countdown expiresAt={request.expires_at} bare /></>}
              {item && !isPending(item) && <> · {item.state}</>}
            </p>
          </div>
          {request && <RiskChip risk={request.class} />}
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
              <h3 className={label}>What it wants to do</h3>
              <div className="mt-1">
                {request.request && <MessageBody body={request.request} />}
                {request.summary && request.summary !== request.request && (
                  <MessageBody body={request.summary} />
                )}
                {card && <CardData request={request} />}
              </div>
            </section>
            <RunCard
              request={request}
              item={item}
              run={item?.action_run ? <ActionRunPanel initial={item.action_run} approved={resolution?.response.decision === "approved"} /> : undefined}
            />
            {request.details_markdown && (
              <section>
                <h3 className={label}>Details</h3>
                <MessageBody body={request.details_markdown} />
              </section>
            )}
            {request.impact && (
              <div className="grid gap-3 sm:grid-cols-2">
                <section className="rounded border border-border p-3">
                  <h3 className={label}>If approved</h3>
                  <div className="mt-1">{bullets(request.impact.approve)}</div>
                </section>
                <section className="rounded border border-border p-3">
                  <h3 className={label}>If denied</h3>
                  <div className="mt-1">{bullets(request.impact.deny)}</div>
                </section>
              </div>
            )}
            {!!(request.evidence ?? item?.evidence)?.length && (
              <section>
                <h3 className={label}>Evidence</h3>
                <div className="mt-2 rounded border border-border">
                  {(request.evidence ?? item?.evidence ?? []).map((e, i) => <EvidenceView key={i} evidence={e} />)}
                </div>
              </section>
            )}
            {request.recommendation && (
              <section>
                <h3 className={label}>Recommendation</h3>
                <div className="mt-1"><MessageBody body={request.recommendation} /></div>
              </section>
            )}
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
            {item && <AuditTrail id={item.item_id} client={client} />}
            {item?.card_receipt && <CardReceiptView receipt={item.card_receipt} />}
          </>
        )}
      </div>
      {/* Decision bar: fixed at the bottom of the detail. */}
      <div role="toolbar" aria-label="Decision" onKeyDown={onBarKey} className="max-h-[40vh] space-y-3 overflow-y-auto border-t border-border bg-bg p-4">
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
            ) : request.approvable === false || handoff ? (
              <section className="rounded border border-border p-4">
                <p className="dash-control text-fg-muted">This request cannot be approved here. Complete the decision in the provider console.</p>
                {ready && safeHTTPS(request.handoff_url) && <a href={safeHTTPS(request.handoff_url)} target="_blank" rel="noopener noreferrer" className="dash-control text-info">Open in {request.source.application_id === "cerberus" ? "Cerberus" : "provider console"}</a>}
              </section>
            ) : (
              <>
                <Textarea
                  aria-label="Decision note"
                  disabled={busy}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  maxLength={8000}
                  rows={2}
                  placeholder={`Optional note or reply to ${agent}…`}
                  className="w-full dash-control"
                />
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
                {denyNeedsReason && <p className="dash-support text-fg-muted">A reason is needed to deny this request. Add it in the note.</p>}
                {needsPhrase && (request.kind === "approval" || request.kind === "card.approval") && (
                  <div className="rounded border border-danger/40 p-3">
                    <p className="dash-control text-danger">This cannot be undone.</p>
                    {item?.destructive?.summary && <p className="mt-1 dash-support text-fg-muted">{item.destructive.summary}</p>}
                    <label className="mt-2 block dash-control text-fg-muted">
                      Type <span className="font-mono text-fg">{phrase}</span> to confirm
                      <input
                        aria-label="Confirmation phrase"
                        autoComplete="off"
                        value={typed}
                        onChange={(e) => setTyped(e.target.value)}
                        className="mt-2 w-full border border-border bg-bg p-2 font-mono dash-control"
                      />
                    </label>
                  </div>
                )}
                {request.kind === "card.info" ? (
                  <p className="dash-control text-fg-muted">This card is for information; there is nothing to answer.</p>
                ) : request.kind === "card.question" || request.kind === "card.form" ? (
                  <CardForm request={request} itemId={item.item_id} client={client} busy={busy} disabled={!canDecide} onSubmit={(answers) => void decide(answers)} />
                ) : null}
                <div className="flex flex-wrap items-center gap-2">
                {item && isPending(item) && !isExpired(item) && (() => {
                  const expiry = request.expires_at ? Date.parse(request.expires_at) : undefined
                  // A snoozed item must not expire unnoticed: offer only presets that end first.
                  const presets = DEFER_PRESETS.filter((p) => expiry === undefined || Date.now() + p.seconds * 1000 < expiry)
                  const dueBefore = expiry !== undefined && item.deferred_until && expiry < Date.parse(item.deferred_until)
                  return item.deferred_until ? (
                    <p className="flex flex-wrap items-center gap-2 dash-control text-fg-muted">
                      Snoozed until {new Date(item.deferred_until).toLocaleString()}.
                      {dueBefore && <span>Expires before this ends.</span>}
                      <Button variant="outline" size="sm" disabled={busy} onClick={() => void bringBack()}>Bring back now</Button>
                    </p>
                  ) : presets.length ? (
                    <DropdownMenu>
                      <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" disabled={busy || !ready} aria-label="Decide later" />}>Defer</DropdownMenuTrigger>
                      <DropdownMenuContent aria-label="Decide later" align="start" data-dashboard-shape="square" className="w-40">
                        {presets.map((p) => (
                          <DropdownMenuItem key={p.label} onClick={() => void defer(p.seconds)}>{p.label}</DropdownMenuItem>
                        ))}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  ) : (
                    <p className="dash-control text-fg-muted">Expires soon, so it cannot be snoozed.</p>
                  )
                })()}
                {(request.kind === "approval" || request.kind === "card.approval") ? (
                  <div className="ml-auto flex flex-wrap gap-2">
                    {item?.alternatives?.map((a) => (
                      <Button key={a.id} variant="outline" disabled={!canDecide} onClick={() => void decide(undefined, "approved", a.id)}>{a.label}</Button>
                    ))}
                    <Button variant="outline" disabled={!canDecide || denyNeedsReason} onClick={() => decideOrHold("denied")}>
                      Deny <Keycap>D</Keycap>
                    </Button>
                    <Button disabled={!canDecide || (needsPhrase && !phraseOk)} onClick={approve}>
                      {busy || isHeld ? "Approving…" : item?.action_run ? "Approve and run" : "Approve"} <Keycap>A</Keycap>
                    </Button>
                  </div>
                ) : !["card.info", "card.question", "card.form"].includes(request.kind) ? (
                  <Button className="ml-auto" disabled={!canDecide} onClick={() => void decide(undefined, "acknowledged")}>
                    Acknowledge
                  </Button>
                ) : null}
                </div>
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
