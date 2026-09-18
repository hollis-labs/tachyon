import { EmptyState } from "@hollis-labs/sysop-ui"
import { MoreVertical, Play, Square, Trash2 } from "lucide-react"
import type { AgentStatus } from "./filter-bar"

export interface Agent {
	id: string
	name: string
	status: AgentStatus
	task: string
	startedAt: string
	duration?: string
}

interface AgentTableProps {
	agents: Agent[]
	onStatusChange?: (id: string, status: AgentStatus) => void
	onDelete?: (id: string) => void
	emptyVariant?: "no-results" | "no-agents"
}

const STATUS_COLORS: Record<AgentStatus, string> = {
	running: "text-status-doing",
	idle: "text-text-subtle",
	failed: "text-status-failed",
	completed: "text-status-done",
}

export function AgentTable({
	agents,
	onStatusChange,
	onDelete,
	emptyVariant = "no-agents",
}: AgentTableProps) {
	if (agents.length === 0) {
		return (
			<EmptyState
				variant={emptyVariant === "no-results" ? "no-results" : "empty"}
				title={emptyVariant === "no-results" ? "No matching agents" : "No agents running"}
				description={
					emptyVariant === "no-results"
						? "Try adjusting your filters or search query"
						: "Agent instances will appear here when they are active"
				}
			/>
		)
	}

	return (
		<div className="divide-y divide-border border-t border-border">
			{agents.map((agent) => (
				<div
					key={agent.id}
					className="flex items-center gap-4 bg-surface px-4 py-3 hover:bg-panel-1 transition-colors"
				>
					{/* Status indicator */}
					<div className={`h-2 w-2 rounded-full ${agent.status === "running" ? "bg-status-doing animate-pulse" : agent.status === "failed" ? "bg-status-failed" : agent.status === "completed" ? "bg-status-done" : "bg-text-subtle"}`} />

					{/* Agent info */}
					<div className="flex-1 min-w-0">
						<div className="flex items-center gap-2">
							<span className="font-medium text-sm text-text truncate">{agent.name}</span>
							<span className={`text-xs uppercase tracking-wider ${STATUS_COLORS[agent.status]}`}>
								{agent.status}
							</span>
						</div>
						<div className="text-xs text-text-subtle mt-0.5 truncate">
							{agent.task}
						</div>
						<div className="flex items-center gap-3 text-xs text-text-subtle mt-1">
							<span>ID: {agent.id}</span>
							<span>Started: {agent.startedAt}</span>
							{agent.duration && <span>Duration: {agent.duration}</span>}
						</div>
					</div>

					{/* Actions */}
					<div className="flex items-center gap-1">
						{agent.status === "running" && onStatusChange && (
							<button
								type="button"
								onClick={() => onStatusChange(agent.id, "idle")}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
								title="Stop agent"
							>
								<Square className="h-4 w-4" />
							</button>
						)}
						{agent.status === "idle" && onStatusChange && (
							<button
								type="button"
								onClick={() => onStatusChange(agent.id, "running")}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
								title="Start agent"
							>
								<Play className="h-4 w-4" />
							</button>
						)}
						{onDelete && (
							<button
								type="button"
								onClick={() => onDelete(agent.id)}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-status-failed transition-colors"
								title="Delete agent"
							>
								<Trash2 className="h-4 w-4" />
							</button>
						)}
						<button
							type="button"
							className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
							title="More actions"
						>
							<MoreVertical className="h-4 w-4" />
						</button>
					</div>
				</div>
			))}
		</div>
	)
}
