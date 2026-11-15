import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import {
  APPROVAL_BASE,
  approvalClient,
  type ApprovalClient,
  type ApprovalInbox,
  type ApprovalItem,
  type ApprovalResponse,
  handoffItems,
  isHandoffError,
} from "./client"
import { noteServerNow } from "./clock"
import { HeldToasts } from "./toasts"
import { ApprovalDialog } from "./dialog"

interface ApprovalContextValue {
  inbox?: ApprovalInbox
  error?: string
  loading: boolean
  refresh(fresh?: boolean): Promise<void>
  /** "live" while the server stream is connected; "down" means the queue refreshes by polling every 10 s. */
  stream: "live" | "down"
  page(view: "pending" | "history", cursor?: string): Promise<void>
  cursors: { pending_before?: string; history_before?: string }
  open(id?: string): void
  client: ApprovalClient
  /** Decisions held for HOLD_MS so they can be undone; committed when the deadline passes. */
  held: HeldDecision[]
  hold(item: ApprovalItem, response: ApprovalResponse): void
  undo(itemId: string): void
  /** Set when a held decision was undone: the page re-selects it with its note. */
  restored?: { itemId: string; note: string }
}
export interface HeldDecision {
  itemId: string
  verb: string
  deadline: number
  item: ApprovalItem
  response: ApprovalResponse
}
export const HOLD_MS = 8000
export const POLL_MS = 10000
const RECONNECT_MS = 5000
// Item events on GET /approval-inbox/events (docs/approvals-contract.md); any of them means the queue changed.
const STREAM_EVENTS = ["item.created", "item.updated", "item.resolved", "item.expired", "item.deferred", "item.undeferred", "resync"]
const MAX_VISIBLE = 3
const Context = createContext<ApprovalContextValue | null>(null)
export function ApprovalProvider({
  children,
  client = approvalClient,
}: {
  children: ReactNode
  client?: ApprovalClient
}) {
  const [inbox, setInbox] = useState<ApprovalInbox>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [stream, setStream] = useState<"live" | "down">("down")
  const streamLive = useRef(false)
  const [selected, setSelected] = useState<string>()
  const cursors = useRef<{ pending_before?: string; history_before?: string }>({})
  const active = useRef(true)
  const sequence = useRef(0)
  const refresh = useCallback(
    async (fresh = false) => {
      const n = ++sequence.current
      try {
        const next = await client.inbox(fresh, cursors.current)
        noteServerNow(next.pending[0]?.server_now_ms ?? next.history[0]?.server_now_ms)
        if (active.current && n === sequence.current) {
          setInbox(next)
          setError(undefined)
        }
      } catch (e) {
        if (active.current && n === sequence.current)
          setError(
            e instanceof Error ? e.message : "Approval queue unavailable",
          )
      } finally {
        if (active.current && n === sequence.current) setLoading(false)
      }
    },
    [client],
  )
  useEffect(() => {
    active.current = true
    // Polling is the fallback: it runs only while the stream is down.
    const timer = setInterval(() => {
      if (!streamLive.current && document.visibilityState !== "hidden") void refresh(true)
    }, POLL_MS)
    let es: EventSource | undefined
    let pending: ReturnType<typeof setTimeout> | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    const changed = () => {
      // Several events can arrive together; one refresh covers them.
      clearTimeout(pending)
      pending = setTimeout(() => void refresh(true), 150)
    }
    // The stream needs the session cookie the first inbox call sets, and the browser gives up
    // for good on a refused stream, so open it after a load and reopen it when it closes.
    const connect = () => {
      if (!active.current || typeof EventSource === "undefined" || es) return
      es = new EventSource(`${APPROVAL_BASE}/events`)
      es.onopen = () => {
        streamLive.current = true
        setStream("live")
        void refresh(true)
      }
      es.onerror = () => {
        streamLive.current = false
        setStream("down")
        if (es?.readyState === 2) {
          es.close()
          es = undefined
          clearTimeout(retry)
          retry = setTimeout(() => void refresh(true).then(connect), RECONNECT_MS)
        }
      }
      for (const name of STREAM_EVENTS) es.addEventListener(name, changed)
    }
    void refresh().then(connect)
    const off = dataRefreshRegistry("approvals").subscribe(() => {
      void refresh()
    })
    return () => {
      active.current = false
      clearInterval(timer)
      clearTimeout(pending)
      clearTimeout(retry)
      es?.close()
      streamLive.current = false
      off()
    }
  }, [refresh])
  const page = async (view: "pending" | "history", cursor?: string) => {
    cursors.current = { ...cursors.current, [`${view}_before`]: cursor }
    setLoading(true)
    await refresh(true)
  }
  const [held, setHeld] = useState<HeldDecision[]>([])
  const heldRef = useRef<HeldDecision[]>([])
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())
  const [notice, setNotice] = useState("")
  const [restored, setRestored] = useState<{ itemId: string; note: string }>()
  const drop = useCallback((id: string) => {
    clearTimeout(timers.current.get(id))
    timers.current.delete(id)
    heldRef.current = heldRef.current.filter((h) => h.itemId !== id)
    setHeld(heldRef.current)
  }, [])
  const commit = useCallback(
    async (h: HeldDecision, keepalive = false) => {
      drop(h.itemId)
      try {
        await client.decide(h.item, h.response, { keepalive })
        setNotice(`${h.response.decision === "approved" ? "Approved" : "Decided"}: ${h.item.request_snapshot.title}`)
      } catch (e) {
        if (isHandoffError(e)) {
          handoffItems.add(h.itemId)
          setNotice("This request cannot be approved here. Complete the decision in the provider console.")
          if (!keepalive) void refresh(true)
          return
        }
        setError(e instanceof Error ? e.message : "Decision not confirmed. Refresh to check the outcome.")
      }
      if (!keepalive) void refresh(true)
    },
    [client, drop, refresh],
  )
  const hold = useCallback(
    (item: ApprovalItem, response: ApprovalResponse) => {
      if (heldRef.current.some((h) => h.itemId === item.item_id)) return
      const entry: HeldDecision = {
        itemId: item.item_id,
        verb: response.decision === "approved" ? "Approving" : "Denying",
        deadline: Date.now() + HOLD_MS,
        item,
        response,
      }
      heldRef.current = [...heldRef.current, entry]
      setHeld(heldRef.current)
      setNotice(`${entry.verb} in ${HOLD_MS / 1000}s. Undo is available.`)
      timers.current.set(item.item_id, setTimeout(() => void commit(entry), HOLD_MS))
      // At most MAX_VISIBLE toasts: the oldest commits first.
      const extra = heldRef.current.length - MAX_VISIBLE
      if (extra > 0) heldRef.current.slice(0, extra).forEach((h) => void commit(h))
    },
    [commit],
  )
  const undo = useCallback(
    (id: string) => {
      const h = heldRef.current.find((x) => x.itemId === id)
      drop(id)
      if (h) setRestored({ itemId: id, note: h.response.note ?? "" })
      setNotice("Undone")
    },
    [drop],
  )
  // Closing or hiding the tab commits what was held: a silent loss is worse than losing undo.
  useEffect(() => {
    const flush = () => heldRef.current.slice().forEach((h) => void commit(h, true))
    const hide = () => {
      if (document.visibilityState === "hidden") flush()
    }
    document.addEventListener("visibilitychange", hide)
    window.addEventListener("pagehide", flush)
    return () => {
      document.removeEventListener("visibilitychange", hide)
      window.removeEventListener("pagehide", flush)
    }
  }, [commit])
  const open = (id?: string) => setSelected(id ?? inbox?.pending[0]?.item_id)
  return (
    <Context.Provider value={{ inbox, error, loading, refresh, stream, page, cursors: cursors.current, open, client, held, hold, undo, restored }}>
      {children}
      <HeldToasts held={held} onUndo={undo} notice={notice} />
      {selected && (
        <ApprovalDialog
          key={selected}
          id={selected}
          client={client}
          initial={[...(inbox?.pending ?? []), ...(inbox?.history ?? [])].find(
            (i) => i.item_id === selected,
          )}
          onClose={() => setSelected(undefined)}
          onChanged={() => {
            void refresh(true)
          }}
        />
      )}
    </Context.Provider>
  )
}
export const useApprovals = () => useContext(Context)
