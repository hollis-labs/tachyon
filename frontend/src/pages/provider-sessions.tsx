import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Input,
  Label,
} from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useCallback, useEffect, useRef, useState } from "react"
import {
  type ConnectionInfo,
  type ProviderSession,
  sessionsApi,
  sessionTime,
} from "../api/sessions"
import type { Envelope } from "../api/verbs"
import { useVerbs } from "../api/verbs"
import { SessionHistoryPanel } from "../components/sessions/history-panel"
import { SessionMutationForm } from "../components/sessions/mutation-form"
import { SessionReadFeedback, useSessionRead } from "../components/sessions/read-state"

function SessionDetail({ id }: { id: string }) {
  const verbs = useVerbs()
  const load = useCallback(() => sessionsApi.read(id), [id])
  const read = useSessionRead(load, verbs.has("session_read"))
  const [connection, setConnection] = useState<Envelope<ConnectionInfo> | null>(null)
  const [connecting, setConnecting] = useState(false)
  const connectionFlight = useRef(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  async function inspectConnection() {
    if (connectionFlight.current) return
    connectionFlight.current = true
    setConnecting(true)
    try {
      const result = await sessionsApi.attach(id)
      if (mounted.current) setConnection(result)
    } catch {
      if (mounted.current)
        setConnection({
          status: "error",
          error: { code: "transport", message: "Connection metadata is unavailable." },
        })
    } finally {
      connectionFlight.current = false
      if (mounted.current) setConnecting(false)
    }
  }
  const session = read.result?.status === "ok" ? read.result.data : null
  return (
    <div className="min-w-0 space-y-4">
      <Button variant="outline" disabled={read.busy} onClick={() => void read.refresh()}>
        Refresh details
      </Button>
      <SessionReadFeedback busy={read.busy} result={read.result} />
      {!read.busy && session && (
        <dl className="grid min-w-0 gap-2 text-sm">
          {Object.entries({
            ID: session.id,
            Launch: session.launch_id,
            Agent: session.agent_id,
            Project: session.project_id,
            Provider: session.provider_id,
            Kind: session.provider_kind,
            State: session.state,
            Created: sessionTime(session.created_at),
            Updated: sessionTime(session.updated_at),
            Ended: sessionTime(session.ended_at),
          }).map(([key, value]) => (
            <div key={key} className="break-all">
              <dt className="text-text-muted">{key}</dt>
              <dd>{value || "Not available"}</dd>
            </div>
          ))}
        </dl>
      )}
      {verbs.has("session_attach") && (
        <section className="space-y-2">
          <Button variant="outline" disabled={connecting} onClick={() => void inspectConnection()}>
            Inspect connection metadata
          </Button>
          <SessionReadFeedback busy={connecting} result={connection} />
          {connection?.status === "ok" && (
            <>
              <pre className="whitespace-pre-wrap break-all text-xs">
                {JSON.stringify(
                  {
                    session_id: connection.data.session_id,
                    provider_id: connection.data.provider_id,
                    state: connection.data.state,
                    transport: connection.data.transport,
                    streaming: connection.data.streaming,
                  },
                  null,
                  2,
                )}
              </pre>
              <p className="text-sm">
                {connection.data.streaming
                  ? "Streaming supported by provider; no stream opened in Tachyon."
                  : "Provider reports no streaming support; no stream opened in Tachyon."}{" "}
                This is connection metadata only.
              </p>
            </>
          )}
        </section>
      )}
      {verbs.has("session_stop") && <SessionMutationForm action="stop" id={id} />}
      {verbs.has("session_submit") && <SessionMutationForm action="submit" id={id} />}
      {verbs.has("session_history") && <SessionHistoryPanel id={id} />}
    </div>
  )
}
export function ProviderSessionsPage() {
  const verbs = useVerbs()
  const [draft, setDraft] = useState({ agent_id: "", provider: "", status: "" })
  const [filters, setFilters] = useState(draft)
  const load = useCallback(() => sessionsApi.list({ ...filters, limit: 50 }), [filters])
  const read = useSessionRead(load, verbs.has("session_list"))
  const [rows, setRows] = useState<ProviderSession[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [extra, setExtra] = useState<Envelope<unknown> | null>(null)
  const [pageBusy, setPageBusy] = useState(false)
  const flight = useRef(false)
  const generation = useRef<typeof filters | null>(null)
  const [dialog, setDialog] = useState<string | null>(null)
  const trigger = useRef<HTMLElement | null>(null)
  useEffect(() => {
    generation.current = filters
    flight.current = false
    setRows([])
    setCursor(undefined)
    setExtra(null)
    setPageBusy(false)
    return () => {
      generation.current = null
    }
  }, [filters])
  useEffect(() => {
    if (read.result?.status === "ok") {
      setRows(read.result.data.sessions ?? [])
      setCursor(read.result.data.next_cursor)
      setExtra(null)
    }
  }, [read.result])
  function open(id: string) {
    trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setDialog(id)
  }
  async function more() {
    if (flight.current || read.busy || !cursor) return
    flight.current = true
    setPageBusy(true)
    const version = generation.current
    try {
      const result = await sessionsApi.list({ ...filters, limit: 50, cursor })
      if (version !== generation.current) return
      if (result.status !== "ok") setExtra(result)
      else if (result.data.next_cursor === cursor)
        setExtra({
          status: "error",
          error: {
            code: "pagination",
            message: "Session continuation did not advance. Refresh the list.",
          },
        })
      else {
        setRows((current) => [
          ...new Map(
            [...current, ...(result.data.sessions ?? [])].map((row) => [row.id, row]),
          ).values(),
        ])
        setCursor(result.data.next_cursor)
        setExtra(null)
      }
    } catch {
      if (version === generation.current)
        setExtra({
          status: "error",
          error: {
            code: "transport",
            message: "More sessions could not be loaded. Previous sessions remain visible.",
          },
        })
    } finally {
      if (version === generation.current) {
        flight.current = false
        setPageBusy(false)
      }
    }
  }
  return (
    <div className="min-w-0 space-y-4">
      <PageHeader title="Active Sessions">
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={read.busy || pageBusy || !verbs.has("session_list")}
            onClick={() => {
              setCursor(undefined)
              void read.refresh()
            }}
          >
            Refresh
          </Button>
          {verbs.has("session_create") && (
            <Button onClick={() => open("create")}>Create session</Button>
          )}
        </div>
      </PageHeader>
      <div className="min-w-0 space-y-4 p-4">
        <p className="text-sm text-text-muted">
          Provider sessions backed by Tether. Durable agents remain on the separate Durable Sessions
          page. Refresh is manual; no live stream.
        </p>
        {verbs.loading ? (
          <p role="status">Discovering session capabilities…</p>
        ) : !verbs.has("session_list") ? (
          <p>Session listing is unavailable.</p>
        ) : (
          <>
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(e) => {
                e.preventDefault()
                setFilters({ ...draft })
              }}
            >
              {(["agent_id", "provider", "status"] as const).map((field) => (
                <div key={field} className="min-w-0 space-y-2">
                  <Label htmlFor={`session-filter-${field}`}>
                    {field === "agent_id"
                      ? "Agent ID"
                      : field === "provider"
                        ? "Provider ID"
                        : "Session state"}
                  </Label>
                  <Input
                    id={`session-filter-${field}`}
                    value={draft[field]}
                    onChange={(e) => setDraft({ ...draft, [field]: e.target.value })}
                  />
                </div>
              ))}
              <Button type="submit" disabled={read.busy || pageBusy}>
                Apply filters
              </Button>
            </form>
            <SessionReadFeedback busy={read.busy} result={read.result} />
            {!read.busy && read.result?.status === "ok" && (
              <>
                {!rows.length && (
                  <EmptyState
                    variant="no-results"
                    title="No sessions on this page"
                    description={
                      cursor
                        ? "More provider pages may contain matching sessions."
                        : "No matching sessions were returned."
                    }
                  />
                )}
                {rows.map((row) => (
                  <article
                    key={row.id}
                    className="min-w-0 space-y-2 rounded border border-border bg-surface p-3"
                  >
                    <h2 className="break-all font-medium">{row.id}</h2>
                    <p className="break-words text-sm">
                      {row.state} · {row.provider_id} · {row.agent_id}
                    </p>
                    <p className="text-xs text-text-muted">Updated {sessionTime(row.updated_at)}</p>
                    {verbs.has("session_read") && (
                      <Button variant="outline" onClick={() => open(row.id)}>
                        Inspect {row.id}
                      </Button>
                    )}
                  </article>
                ))}
                <SessionReadFeedback busy={pageBusy} result={extra} />
                {!!cursor && (
                  <Button
                    variant="outline"
                    disabled={pageBusy || extra?.status === "ask"}
                    onClick={() => void more()}
                  >
                    Load more sessions
                  </Button>
                )}
              </>
            )}
          </>
        )}
      </div>
      <Dialog
        open={dialog !== null}
        onOpenChange={(next) => {
          if (!next) setDialog(null)
        }}
      >
        <DialogContent
          finalFocus={trigger}
          className="max-h-[calc(100vh-2rem)] overflow-y-auto"
          widthClassName="max-w-3xl"
        >
          <DialogHeader>
            <DialogTitle>
              {dialog === "create" ? "Create provider session" : `Session ${dialog ?? ""}`}
            </DialogTitle>
          </DialogHeader>
          {dialog === "create" ? (
            <SessionMutationForm action="create" />
          ) : (
            dialog && <SessionDetail key={dialog} id={dialog} />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
