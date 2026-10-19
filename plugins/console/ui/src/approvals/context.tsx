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
  approvalClient,
  type ApprovalClient,
  type ApprovalInbox,
} from "./client"
import { ApprovalDialog } from "./dialog"

interface ApprovalContextValue {
  inbox?: ApprovalInbox
  error?: string
  loading: boolean
  refresh(fresh?: boolean): Promise<void>
  page(view: "pending" | "history", cursor?: string): Promise<void>
  cursors: { pending_before?: string; history_before?: string }
  open(id?: string): void
  client: ApprovalClient
}
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
  const [selected, setSelected] = useState<string>()
  const cursors = useRef<{ pending_before?: string; history_before?: string }>({})
  const active = useRef(true)
  const sequence = useRef(0)
  const refresh = useCallback(
    async (fresh = false) => {
      const n = ++sequence.current
      try {
        const next = await client.inbox(fresh, cursors.current)
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
    void refresh()
    const timer = setInterval(() => {
      if (document.visibilityState !== "hidden") void refresh(true)
    }, 10000)
    const off = dataRefreshRegistry("approvals").subscribe(() => {
      void refresh()
    })
    return () => {
      active.current = false
      clearInterval(timer)
      off()
    }
  }, [refresh])
  const page = async (view: "pending" | "history", cursor?: string) => {
    cursors.current = { ...cursors.current, [`${view}_before`]: cursor }
    setLoading(true)
    await refresh(true)
  }
  const open = (id?: string) => setSelected(id ?? inbox?.pending[0]?.item_id)
  return (
    <Context.Provider value={{ inbox, error, loading, refresh, page, cursors: cursors.current, open, client }}>
      {children}
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
