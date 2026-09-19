import { notifyError, Skeleton } from "@hollis-labs/sysop-ui"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { AgentTool } from "../../api/client"
import { useApi } from "../../api/context"
import { type ChecklistItem, SearchableChecklist } from "./checklist"

interface AgentToolsPanelProps {
  agentId: string
}

// Nanite's /api/agents/{id}/tools has no per-tool "which MCP server" field
// (only a coarse known_tools.source of builtin|mcp|plugin, which the
// endpoint doesn't even return) — a tool's name prefix is the only signal
// available client-side for grouping, so the filter below is a naming
// heuristic, not a literal MCP-server association.
function categoryOf(toolName: string): string {
  const idx = toolName.indexOf("_")
  return idx === -1 ? toolName : toolName.slice(0, idx)
}

export function AgentToolsPanel({ agentId }: AgentToolsPanelProps) {
  const api = useApi()
  const [tools, setTools] = useState<AgentTool[]>([])
  const [loading, setLoading] = useState(true)
  const [pendingId, setPendingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setLoading(true)
      setTools(await api.listAgentTools(agentId))
    } catch (error) {
      notifyError(error, "Failed to load tools")
    } finally {
      setLoading(false)
    }
  }, [api, agentId])

  useEffect(() => {
    load()
  }, [load])

  const items: ChecklistItem[] = useMemo(
    () =>
      tools.map((t) => ({
        value: t.id || t.name,
        label: t.name,
        description: t.description,
        category: categoryOf(t.name),
        disabled: !t.id,
        disabledReason: t.id
          ? undefined
          : "Not yet synced into the provider's known-tools catalog — needs a restart",
      })),
    [tools],
  )
  const checked = useMemo(
    () => new Set(tools.filter((t) => t.granted && t.id).map((t) => t.id)),
    [tools],
  )
  const ungrantableCount = tools.filter((t) => !t.id).length

  async function handleToggle(toolId: string, next: boolean) {
    setPendingId(toolId)
    try {
      if (next) {
        await api.grantAgentTool(agentId, toolId)
      } else {
        await api.revokeAgentTool(agentId, toolId)
      }
    } catch (error) {
      notifyError(error, next ? "Failed to grant tool" : "Failed to revoke tool")
    } finally {
      await load()
      setPendingId(null)
    }
  }

  if (loading) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full rounded-md" />
        ))}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <SearchableChecklist
        items={items}
        checked={checked}
        onToggle={handleToggle}
        searchPlaceholder="Search tools by name or description…"
        emptyText="No tools match"
        categoryLabel="categories"
      />
      {pendingId && <p className="text-xs text-text-subtle">Updating grant…</p>}
      {ungrantableCount > 0 && (
        <p className="text-xs text-text-subtle">
          {ungrantableCount} tool{ungrantableCount === 1 ? "" : "s"} greyed out — not yet grantable
          until the provider restarts and syncs {ungrantableCount === 1 ? "it" : "them"} into its
          known-tools catalog.
        </p>
      )}
    </div>
  )
}
