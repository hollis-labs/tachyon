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
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <div className="flex items-center gap-3">
            <FileText className="h-6 w-6 shrink-0 text-sky-300" />
            <DialogTitle className="pr-5 font-mono text-sm">
              {session.title || "Untitled session"}
            </DialogTitle>
          </div>
          <DialogDescription className="font-mono text-xs">
            {session.tool} · {session.model || "Model unspecified"} ·{" "}
            {session.kind}
          </DialogDescription>
        </DialogHeader>
        <dl className="grid gap-3 border-y border-zinc-800 py-3 text-xs sm:grid-cols-2">
          <div>
            <dt className="text-zinc-500">Project</dt>
            <dd className="mt-1 break-all text-zinc-300">
              {session.project || "Unspecified"}
            </dd>
          </div>
          <div>
            <dt className="text-zinc-500">Started</dt>
            <dd className="mt-1 text-zinc-300">
              {session.started_at
                ? new Date(session.started_at).toLocaleString()
                : "Unknown"}
            </dd>
          </div>
          <div>
            <dt className="text-zinc-500">Recorded activity</dt>
            <dd className="mt-1 text-zinc-300">
              {number(session.turns)} turns · {number(session.tool_calls)} tool
              calls · {number(session.errors)} tool errors
            </dd>
          </div>
          <div>
            <dt className="text-zinc-500">Session ID</dt>
            <dd className="mt-1 break-all font-mono text-[10px] text-zinc-400">
              {session.id}
            </dd>
          </div>
        </dl>
        {detail?.usage && (
          <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
            {Object.entries({
              "Input tokens": detail.usage.input_tokens,
              "Output tokens": detail.usage.output_tokens,
              "Cache reads": detail.usage.cache_read_tokens,
              "Cache writes": detail.usage.cache_creation_tokens,
            }).map(([label, value]) => (
              <div key={label}>
                <div className="text-zinc-500">{label}</div>
                <div className="mt-1 font-mono text-zinc-200">
                  {number(value ?? 0)}
                </div>
              </div>
            ))}
          </div>
        )}
        {error && (
          <div role="alert" className="text-xs text-red-300">
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
          <p role="status" className="py-6 text-sm text-zinc-500">
            Loading transcript…
          </p>
        )}
        {detail && (
          <>
            <div className="flex items-center justify-between text-xs text-zinc-500">
              <span>
                {detail.turns.length
                  ? `Turns ${offset + 1}–${offset + detail.turns.length} of ${number(detail.turn_total)}`
                  : "No recorded turns"}
              </span>
              <span>Fragments Engine · redacted transcript</span>
            </div>
            <div className="divide-y divide-zinc-800">
              {detail.turns.map((turn) => (
                <article key={turn.seq} className="py-4">
                  <div className="mb-2 flex items-center gap-3 font-mono text-[10px] uppercase tracking-wide">
                    <span
                      className={
                        turn.role === "user" ? "text-amber-300" : "text-sky-300"
                      }
                    >
                      {turn.role}
                    </span>
                    <span className="text-zinc-600">#{turn.seq}</span>
                    {turn.ts && (
                      <time className="text-zinc-500">
                        {new Date(turn.ts).toLocaleTimeString()}
                      </time>
                    )}
                  </div>
                  {turn.text ? (
                    <MessageBody body={turn.text} />
                  ) : (
                    <p className="text-xs italic text-zinc-500">
                      No text recorded for this event.
                    </p>
                  )}
                </article>
              ))}
            </div>
            <div className="flex items-center justify-between border-t border-zinc-800 pt-3">
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
