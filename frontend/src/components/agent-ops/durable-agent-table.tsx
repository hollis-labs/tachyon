import { EmptyState } from "@hollis-labs/sysop-ui"
import type { AgentStatus } from "./filter-bar"

export interface DurableAgentRow {
  id: string
  name: string
  slug?: string
  provider?: string
  model?: string
  status: AgentStatus
  currentSessionId?: string
  updatedAt?: string
}

interface DurableAgentTableProps {
  agents: DurableAgentRow[]
  onRowClick?: (agent: DurableAgentRow) => void
  emptyVariant?: "no-results" | "no-agents"
}

// active = the healthy "running" state; failed calls out something that
// needs attention; everything else (sleeping, starting, paused, stopped,
// the *_requested transitional states, archived) reads as neutral at a
// glance — the row's own status label still names it exactly.
const STATUS_DOT: Record<string, string> = {
  active: "bg-status-done",
  failed: "bg-status-failed",
}

const STATUS_TEXT: Record<string, string> = {
  active: "text-status-done",
  failed: "text-status-failed",
}

export function DurableAgentTable({
  agents,
  onRowClick,
  emptyVariant = "no-agents",
}: DurableAgentTableProps) {
  if (agents.length === 0) {
    return (
      <EmptyState
        variant={emptyVariant === "no-results" ? "no-results" : "empty"}
        title={emptyVariant === "no-results" ? "No matching sessions" : "No durable agents running"}
        description={
          emptyVariant === "no-results"
            ? "Try adjusting your filters or search query"
            : "Launch a long-running agent to see it appear here"
        }
      />
    )
  }

  return (
    <div className="divide-y divide-border border-t border-border">
      {agents.map((agent) => (
        <div
          key={agent.id}
          onClick={() => onRowClick?.(agent)}
          className="flex items-center gap-4 bg-surface px-4 py-3 hover:bg-panel-1 transition-colors cursor-pointer"
        >
          <div className={`h-2 w-2 rounded-full ${STATUS_DOT[agent.status] || "bg-text-subtle"}`} />

          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-medium text-sm text-text truncate">{agent.name}</span>
              <span
                className={`text-xs uppercase tracking-wider ${STATUS_TEXT[agent.status] || "text-text-subtle"}`}
              >
                {agent.status}
              </span>
              {agent.provider && (
                <span className="text-xs text-text-subtle bg-panel-2 px-1.5 py-0.5 rounded">
                  {agent.provider}
                </span>
              )}
              {agent.model && (
                <span className="text-xs text-text-subtle bg-panel-2 px-1.5 py-0.5 rounded">
                  {agent.model}
                </span>
              )}
            </div>
            <div className="flex items-center gap-3 text-xs text-text-subtle mt-1">
              <span>ID: {agent.id}</span>
              {agent.slug && <span>Slug: {agent.slug}</span>}
              {agent.currentSessionId && <span>Session: {agent.currentSessionId}</span>}
              {agent.updatedAt && (
                <span>Updated: {new Date(agent.updatedAt).toLocaleString()}</span>
              )}
            </div>
          </div>
        </div>
      ))}
    </div>
  )
}
