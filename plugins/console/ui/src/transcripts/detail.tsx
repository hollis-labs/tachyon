import { useEffect, useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@hollis-labs/design-components"
import { FileText } from "lucide-react"
import { MessageBody } from "../chat/MessageBody"
import { read, number, type Session, type Detail } from "./data"

export function TranscriptDialog({
  session,
  onClose,
}: {
  session: Session
  onClose(): void
}) {
  const [offset, setOffset] = useState(0)
  const [detail, setDetail] = useState<Detail>()
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setDetail(undefined)
    setError("")
    void read<Detail>(
      `/sessions/${session.id}?turn_offset=${offset}`,
      attempt > 0,
      controller.signal,
    )
      .then((d) => {
        if (!controller.signal.aborted) setDetail(d)
      })
      .catch((e) => {
        if (!controller.signal.aborted)
          setError(
            e instanceof Error ? e.message : "Could not load transcript.",
          )
      })
    return () => controller.abort()
  }, [session.id, offset, attempt])
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className="dash-detail-dialog max-h-[90vh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <div className="flex items-center gap-3">
            <FileText className="h-6 w-6 shrink-0 text-info" />
            <DialogTitle className="pr-5 font-mono dash-body">
              {session.title || "Untitled session"}
            </DialogTitle>
          </div>
          <DialogDescription className="font-mono dash-control">
            {session.tool} · {session.model || "Model unspecified"} ·{" "}
            {session.kind}
          </DialogDescription>
        </DialogHeader>
        <dl className="grid gap-3 border-y border-border py-3 dash-control sm:grid-cols-2">
          <div>
            <dt className="text-fg-faint">Project</dt>
            <dd className="mt-1 break-all text-fg-muted">
              {session.project || "Unspecified"}
            </dd>
          </div>
          <div>
            <dt className="text-fg-faint">Started</dt>
            <dd className="mt-1 text-fg-muted">
              {session.started_at
                ? new Date(session.started_at).toLocaleString()
                : "Unknown"}
            </dd>
          </div>
          <div>
            <dt className="text-fg-faint">Recorded activity</dt>
            <dd className="mt-1 text-fg-muted">
              {number(session.turns)} turns · {number(session.tool_calls)} tool
              calls · {number(session.errors)} tool errors
            </dd>
          </div>
          <div>
            <dt className="text-fg-faint">Session ID</dt>
            <dd className="mt-1 break-all font-mono dash-caption text-fg-muted">
              {session.id}
            </dd>
          </div>
        </dl>
        {detail?.usage && (
          <div className="grid grid-cols-2 gap-3 dash-control sm:grid-cols-4">
            {Object.entries({
              "Input tokens": detail.usage.input_tokens,
              "Output tokens": detail.usage.output_tokens,
              "Cache reads": detail.usage.cache_read_tokens,
              "Cache writes": detail.usage.cache_creation_tokens,
            }).map(([label, value]) => (
              <div key={label}>
                <div className="text-fg-faint">{label}</div>
                <div className="mt-1 font-mono text-fg">
                  {number(value ?? 0)}
                </div>
              </div>
            ))}
          </div>
        )}
        {error && (
          <div role="alert" className="dash-control text-danger">
            {error}{" "}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setAttempt((n) => n + 1)}
            >
              Retry
            </Button>
          </div>
        )}
        {!detail && !error && (
          <p role="status" className="py-6 dash-body text-fg-faint">
            Loading transcript…
          </p>
        )}
        {detail && (
          <>
            <div className="flex items-center justify-between dash-control text-fg-faint">
              <span>
                {detail.turns.length
                  ? `Turns ${offset + 1}–${offset + detail.turns.length} of ${number(detail.turn_total)}`
                  : "No recorded turns"}
              </span>
              <span>Fragments Engine · redacted transcript</span>
            </div>
            <div className="divide-y divide-border">
              {detail.turns.map((turn) => (
                <article key={turn.seq} className="py-4">
                  <div className="mb-2 flex items-center gap-3 font-mono dash-caption uppercase tracking-wide">
                    <span
                      className={
                        turn.role === "user" ? "text-warning" : "text-info"
                      }
                    >
                      {turn.role}
                    </span>
                    <span className="text-fg-faint">#{turn.seq}</span>
                    {turn.ts && (
                      <time className="text-fg-faint">
                        {new Date(turn.ts).toLocaleTimeString()}
                      </time>
                    )}
                  </div>
                  {turn.text ? (
                    <MessageBody body={turn.text} />
                  ) : (
                    <p className="dash-control italic text-fg-faint">
                      No text recorded for this event.
                    </p>
                  )}
                </article>
              ))}
            </div>
            <div className="flex items-center justify-between border-t border-border pt-3">
              <Button
                variant="outline"
                size="sm"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - 50))}
              >
                Previous turns
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={
                  detail.next_turn == null ||
                  detail.next_turn <= offset ||
                  detail.next_turn >= detail.turn_total
                }
                onClick={() => setOffset(detail.next_turn!)}
              >
                Next turns
              </Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
