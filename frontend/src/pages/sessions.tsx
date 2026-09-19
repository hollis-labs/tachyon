import { PageHeader, Skeleton, SummaryCards } from "@hollis-labs/sysop-ui"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { DurableAgent } from "../api/client"
import { useApi } from "../api/context"
import { DurableAgentInspectDialog } from "../components/agent-ops/durable-agent-inspect-dialog"
import {
  type DurableAgentRow,
  DurableAgentTable,
} from "../components/agent-ops/durable-agent-table"
import { type AgentStatus, FilterBar } from "../components/agent-ops/filter-bar"

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-2 p-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-16 w-full rounded-md" />
      ))}
    </div>
  )
}

// Sessions: at-a-glance status for durable (long-running) agent instances —
// running, idle/paused, sleeping — with click-through to inspect one.
// MVP is read-only; start/stop/pause lifecycle actions are post-MVP.
export function SessionsPage() {
  const api = useApi()
  const [agents, setAgents] = useState<DurableAgent[]>([])
  const [loading, setLoading] = useState(true)
  const [activeStatuses, setActiveStatuses] = useState<AgentStatus[]>([])
  const [search, setSearch] = useState<string>("")
  const [inspectId, setInspectId] = useState<string | null>(null)

  const loadAgents = useCallback(async () => {
    try {
      setLoading(true)
      const result = await api.listDurableAgents()
      setAgents(result)
    } catch (error) {
      console.error("Failed to load durable agents:", error)
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    loadAgents()
  }, [loadAgents])

  const availableStatuses = useMemo(() => {
    return Array.from(new Set(agents.map((a) => a.status))).sort()
  }, [agents])

  useEffect(() => {
    if (agents.length > 0 && activeStatuses.length === 0) {
      setActiveStatuses(availableStatuses)
    }
  }, [agents, availableStatuses, activeStatuses.length])

  function handleStatusToggle(status: AgentStatus) {
    setActiveStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status],
    )
  }

  function handleClearFilters() {
    setSearch("")
    setActiveStatuses(availableStatuses)
  }

  const filteredAgents = useMemo(() => {
    let result = agents.filter((a) => activeStatuses.includes(a.status))
    if (search) {
      const query = search.toLowerCase()
      result = result.filter(
        (a) =>
          a.name.toLowerCase().includes(query) ||
          a.id.toLowerCase().includes(query) ||
          (a.slug && a.slug.toLowerCase().includes(query)) ||
          (a.provider && a.provider.toLowerCase().includes(query)) ||
          (a.model && a.model.toLowerCase().includes(query)),
      )
    }
    return result
  }, [agents, activeStatuses, search])

  const rows: DurableAgentRow[] = filteredAgents.map((a) => ({
    id: a.id,
    name: a.name,
    slug: a.slug,
    provider: a.provider,
    model: a.model,
    status: a.status,
    currentSessionId: a.current_session_id,
    updatedAt: a.updated_at,
  }))

  const activeFilterCount =
    (activeStatuses.length !== availableStatuses.length ? 1 : 0) + (search ? 1 : 0)
  const searchMatchCount = search ? filteredAgents.length : undefined
  const emptyVariant = activeFilterCount > 0 || search.length > 0 ? "no-results" : "no-agents"

  const totalCount = agents.length
  const activeCount = agents.filter((a) => a.status === "active").length
  const sleepingCount = agents.filter((a) => a.status === "sleeping").length
  const failedCount = agents.filter((a) => a.status === "failed").length

  const summaryCards = [
    { label: "Total", value: totalCount },
    { label: "Active", value: activeCount, accentColor: "#34d399" },
    { label: "Sleeping", value: sleepingCount },
    ...(failedCount > 0 ? [{ label: "Failed", value: failedCount, accentColor: "#f87171" }] : []),
  ]

  const inspectAgent = inspectId ? (agents.find((a) => a.id === inspectId) ?? null) : null

  return (
    <div className="flex h-full flex-col">
      <PageHeader title="Sessions" />
      {!loading && <SummaryCards cards={summaryCards} />}
      <FilterBar
        activeStatuses={activeStatuses}
        onStatusToggle={handleStatusToggle}
        searchQuery={search}
        onSearchChange={setSearch}
        searchMatchCount={searchMatchCount}
        activeFilterCount={activeFilterCount}
        onClear={handleClearFilters}
        availableStatuses={availableStatuses}
      />
      <div className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : (
          <DurableAgentTable
            agents={rows}
            onRowClick={(row) => setInspectId(row.id)}
            emptyVariant={emptyVariant}
          />
        )}
      </div>
      <DurableAgentInspectDialog agent={inspectAgent} onClose={() => setInspectId(null)} />
    </div>
  )
}
