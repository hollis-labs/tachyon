import { Button, EmptyState } from "@hollis-labs/design-components"
import { useCallback, useEffect, useRef, useState } from "react"
import { type HistoryEvent, sessionsApi, sessionTime } from "../../api/sessions"
import type { Envelope } from "../../api/verbs"
import { SessionReadFeedback, useSessionRead } from "./read-state"

export function SessionHistoryPanel({ id, sinceSeq = 0 }: { id: string; sinceSeq?: number }) {
  const load = useCallback(
    () => sessionsApi.history({ id, since_seq: sinceSeq, limit: 50 }),
    [id, sinceSeq],
  )
  const read = useSessionRead(load, !!id)
  const [events, setEvents] = useState<HistoryEvent[]>([])
  const [cursor, setCursor] = useState<number | undefined>()
  const [pageResult, setPageResult] = useState<Envelope<unknown> | null>(null)
  const [pageBusy, setPageBusy] = useState(false)
  const flight = useRef(false)
  const generation = useRef<{ id: string; sinceSeq: number } | null>(null)
  useEffect(() => {
    generation.current = { id, sinceSeq }
    setEvents([])
    setCursor(undefined)
    setPageResult(null)
    setPageBusy(false)
    flight.current = false
    return () => {
      generation.current = null
    }
  }, [id, sinceSeq])
  useEffect(() => {
    if (read.result?.status === "ok") {
      setEvents(read.result.data.events ?? [])
      setCursor(read.result.data.next_cursor)
      setPageResult(null)
    }
  }, [read.result])
  async function more() {
    if (flight.current || read.busy || !cursor) return
    flight.current = true
    setPageBusy(true)
    const version = generation.current
    try {
      const next = await sessionsApi.history({ id, since_seq: sinceSeq, limit: 50, cursor })
      if (version !== generation.current) return
      if (next.status !== "ok") setPageResult(next)
      else if (next.data.next_cursor && next.data.next_cursor <= cursor) {
        setPageResult({
          status: "error",
          error: {
            code: "pagination",
            message: "History continuation did not advance. Refresh history.",
          },
        })
      } else {
        setEvents((current) => [
          ...new Map(
            [...current, ...(next.data.events ?? [])].map((event) => [event.seq, event]),
          ).values(),
        ])
        setCursor(next.data.next_cursor)
        setPageResult(null)
      }
    } catch {
      if (version === generation.current)
        setPageResult({
          status: "error",
          error: {
            code: "transport",
            message: "More history could not be loaded. Previous events remain visible.",
          },
        })
    } finally {
      if (version === generation.current) {
        setPageBusy(false)
        flight.current = false
      }
    }
  }
  return (
    <section aria-label="Session events" className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="min-w-0 break-all font-medium">History for {id}</h2>
        <Button
          variant="outline"
          disabled={read.busy || pageBusy}
          onClick={() => {
            setCursor(undefined)
            void read.refresh()
          }}
        >
          Refresh history
        </Button>
      </div>
      <p className="text-sm text-text-muted">
        Provider event history. No live stream is opened or stored by Tachyon.
      </p>
      <SessionReadFeedback busy={read.busy} result={read.result} />
      {!read.busy && read.result?.status === "ok" && (
        <>
          {!events.length && (
            <EmptyState
              variant="no-results"
              title="No session events"
              description={
                cursor
                  ? "This page is empty; more events may be available."
                  : "No events were returned by the provider."
              }
            />
          )}
          {events.map((event) => (
            <article
              key={event.seq}
              className="min-w-0 space-y-2 rounded border border-border bg-surface p-3"
            >
              <p className="break-words">
                Sequence {event.seq} · {event.kind} · {event.scope}
              </p>
              <p className="text-xs text-text-muted">{sessionTime(event.timestamp)}</p>
              {event.payload_json && (
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">
                  {event.payload_json}
                </pre>
              )}
            </article>
          ))}
          <SessionReadFeedback busy={pageBusy} result={pageResult} />
          {!!cursor && (
            <Button
              variant="outline"
              disabled={pageBusy || pageResult?.status === "ask"}
              onClick={() => void more()}
            >
              Load more events
            </Button>
          )}
        </>
      )}
    </section>
  )
}
