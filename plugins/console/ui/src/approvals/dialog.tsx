import { useCallback, useEffect, useState } from "react"
import {
  Button,
  Textarea,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@hollis-labs/design-components"
import { ShieldCheck } from "lucide-react"
import { MessageBody } from "../chat/MessageBody"
import {
  isExpired,
  isPending,
  type ApprovalClient,
  type ApprovalItem,
  type ApprovalResponse,
  type Evidence,
} from "./client"

const label = "font-mono dash-caption uppercase tracking-widest text-fg-faint"
function EvidenceView({
  evidence,
  index,
  id,
  client,
}: {
  evidence: Evidence
  index: number
  id: string
  client: ApprovalClient
}) {
  const [preview, setPreview] = useState<Record<string, unknown>>()
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  async function load() {
    setBusy(true)
    setError("")
    try {
      setPreview(await client.evidence(id, index, evidence.type))
    } catch (e) {
      setError(e instanceof Error ? e.message : "Evidence unavailable")
    } finally {
      setBusy(false)
    }
  }
  const rawURL =
    typeof preview?.external_url === "string" ? preview.external_url : ""
  let safeURL: string | undefined
  try {
    const u = new URL(rawURL)
    if (u.protocol === "https:" && !u.username && !u.password) safeURL = u.href
  } catch {
    /* no link */
  }
  return (
    <details className="border-t border-border py-3">
      <summary className="cursor-pointer dash-control text-fg-muted">
        {evidence.label}{" "}
        <span className="text-fg-faint">· {evidence.type}</span>
      </summary>
      {evidence.type === "markdown" && (
        <MessageBody body={evidence.content ?? ""} />
      )}
      {["text", "diff"].includes(evidence.type) && (
        <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-bg p-3 font-mono dash-support text-fg-muted">
          {evidence.content}
        </pre>
      )}
      {!["markdown", "text", "diff"].includes(evidence.type) && (
        <>
          <pre className="my-2 overflow-auto whitespace-pre-wrap break-words font-mono dash-caption text-fg-faint">
            {JSON.stringify(evidence, null, 2)}
          </pre>
          {["tangent_reference", "artifact_ref"].includes(evidence.type) && (
            <Button variant="outline" size="sm"
              className="font-mono dash-label"
              disabled={busy}
              onClick={() => void load()}
            >
              {busy ? "Loading…" : "Load evidence"}
            </Button>
          )}
          {preview && (
            <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words dash-control text-fg-muted">
              {typeof preview.content === "string"
                ? preview.content
                : JSON.stringify(preview, null, 2)}
            </pre>
          )}
          {safeURL && (
            <a
              href={safeURL}
              target="_blank"
              rel="noopener noreferrer"
              className="dash-control text-info"
            >
              Open evidence
            </a>
          )}
        </>
      )}
      {error && (
        <p role="alert" className="mt-2 dash-control text-danger">
          {error}
        </p>
      )}
    </details>
  )
}

export function ApprovalDialog({
  id,
  initial,
  client,
  onClose,
  onChanged,
}: {
  id: string
  initial?: ApprovalItem
  client: ApprovalClient
  onClose(): void
  onChanged(): void
}) {
  const [item, setItem] = useState(initial)
  const [ready, setReady] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [note, setNote] = useState("")
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
  async function decide() {
    if (!item || !choice || busy || !ready) return
    setBusy(true)
    setError("")
    try {
      const outcome = await client.decide(item, {
        kind: item.request_snapshot.kind,
        decision: choice,
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
  const request = item?.request_snapshot
  const resolution = item?.terminal_outcome?.resolution
  const canDecide =
    item && isPending(item) && !isExpired(item) && ready && !busy
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="dash-detail-dialog max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-4">
            <span className="border-2 border-border bg-bg p-3 text-warning">
              <ShieldCheck className="h-8 w-8" />
            </span>
            <div className="min-w-0">
              <DialogTitle className="font-mono dash-body uppercase tracking-wide">
                {request?.title ?? "Review request"}
              </DialogTitle>
              <DialogDescription className="mt-1 font-mono dash-support">
                {request?.source.agent_label ?? request?.source.agent_id} ·{" "}
                {request?.source.application_label ??
                  request?.source.application_id}
              </DialogDescription>
              <p className="mt-1 font-mono dash-caption uppercase tracking-widest text-warning">
                {item?.state ?? "Loading"}{" "}
                {item && isExpired(item) && isPending(item) ? "· expired" : ""}
              </p>
            </div>
          </div>
        </DialogHeader>
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
              <MessageBody body={request.request} />
              {request.summary !== request.request && (
                <MessageBody body={request.summary} />
              )}
            </section>
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
            <dl className="grid grid-cols-2 gap-4 dash-support text-fg-muted">
              <div>
                <dt className={label}>Requested</dt>
                <dd>{new Date(item!.enqueued_at).toLocaleString()}</dd>
              </div>
              <div>
                <dt className={label}>Expires</dt>
                <dd>
                  {request.expires_at
                    ? new Date(request.expires_at).toLocaleString()
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
                    index={i}
                    id={id}
                    client={client}
                  />
                ))}
              </section>
            )}
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
              </section>
            ) : item?.terminal_outcome ? (
              <MessageBody
                body={
                  item.terminal_outcome.reason ??
                  item.terminal_outcome.message ??
                  `Request ${item.state}`
                }
              />
            ) : (
              <>
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
                {choice ? (
                  <section className="rounded border border-warning/30 bg-warning-muted/10 p-3">
                    <p className="dash-control text-fg-muted">
                      Confirm{" "}
                      {choice === "approved"
                        ? "approval"
                        : choice === "denied"
                          ? "denial"
                          : "acknowledgement"}{" "}
                      of this request?
                    </p>
                    <div className="mt-3 flex gap-2">
                      <Button variant="outline" size="sm"
                        className="font-mono dash-label"
                        disabled={!canDecide}
                        onClick={() => void decide()}
                      >
                        {busy ? "Submitting…" : "Confirm decision"}
                      </Button>
                      <Button variant="outline" size="sm"
                        className="font-mono dash-label"
                        disabled={busy}
                        onClick={() => setChoice(undefined)}
                      >
                        Back
                      </Button>
                    </div>
                  </section>
                ) : (
                  <div className="flex gap-2">
                    {request.kind === "approval" ? (
                      <>
                        <Button variant="outline" size="sm"
                          className={`font-mono dash-label text-success`}
                          disabled={!canDecide}
                          onClick={() => setChoice("approved")}
                        >
                          Approve
                        </Button>
                        <Button variant="outline" size="sm"
                          className={`font-mono dash-label text-danger`}
                          disabled={!canDecide}
                          onClick={() => setChoice("denied")}
                        >
                          Deny
                        </Button>
                      </>
                    ) : (
                      <Button variant="outline" size="sm"
                        className="font-mono dash-label"
                        disabled={!canDecide}
                        onClick={() => setChoice("acknowledged")}
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
          </>
        )}
        <div className="flex justify-between border-t border-border pt-3">
          <Button variant="outline" size="sm"
            className="font-mono dash-label"
            disabled={busy}
            onClick={() => void refresh()}
          >
            Refresh request
          </Button>
          <Button variant="outline" size="sm" className="font-mono dash-label" disabled={busy} onClick={onClose}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
