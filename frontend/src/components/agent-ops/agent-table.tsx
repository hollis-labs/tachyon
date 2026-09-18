import { EmptyState } from "@hollis-labs/sysop-ui"
import { Edit, Play, Trash2 } from "lucide-react"
import type { AgentStatus } from "./filter-bar"

export interface Agent {
	id: string
	name: string
	slug?: string
	description?: string
	tags?: string
	icon?: string
	status: AgentStatus
	layer?: string
	editable: boolean
	system_prompt?: string
}

interface AgentTableProps {
	agents: Agent[]
	onEdit?: (id: string) => void
	onDelete?: (id: string) => void
	onLaunch?: (id: string) => void
	onRowClick?: (agent: Agent) => void
	emptyVariant?: "no-results" | "no-agents"
}

const STATUS_COLOR_MAP: Record<string, string> = {
	active: "text-status-done",
	sleeping: "text-text-subtle",
	enabled: "text-status-done",
	disabled: "text-text-subtle",
}

const FALLBACK_STATUS_COLOR = "text-text-soft"

export function AgentTable({
	agents,
	onEdit,
	onDelete,
	onLaunch,
	onRowClick,
	emptyVariant = "no-agents",
}: AgentTableProps) {
	if (agents.length === 0) {
		return (
			<EmptyState
				variant={emptyVariant === "no-results" ? "no-results" : "empty"}
				title={emptyVariant === "no-results" ? "No matching agents" : "No agents configured"}
				description={
					emptyVariant === "no-results"
						? "Try adjusting your filters or search query"
						: "Use the '+ New Agent' button to create your first agent profile"
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
					{/* Status indicator */}
					<div className={`h-2 w-2 rounded-full ${agent.status === "active" ? "bg-status-done" : "bg-text-subtle"}`} />

					{/* Agent info */}
					<div className="flex-1 min-w-0">
						<div className="flex items-center gap-2">
							<span className="font-medium text-sm text-text truncate">{agent.name}</span>
							<span className={`text-xs uppercase tracking-wider ${STATUS_COLOR_MAP[agent.status] || FALLBACK_STATUS_COLOR}`}>
								{agent.status}
							</span>
							{agent.tags && (
								<span className="text-xs text-text-subtle bg-panel-2 px-1.5 py-0.5 rounded">
									{agent.tags}
								</span>
							)}
						</div>
						{agent.description && (
							<div className="text-xs text-text-subtle mt-0.5 truncate">
								{agent.description}
							</div>
						)}
						<div className="flex items-center gap-3 text-xs text-text-subtle mt-1">
							<span>ID: {agent.id}</span>
							{agent.slug && <span>Slug: {agent.slug}</span>}
							{agent.layer && <span>Layer: {agent.layer}</span>}
						</div>
					</div>

					{/* Actions */}
					<div className="flex items-center gap-1">
						{agent.editable && onEdit && (
							<button
								type="button"
								onClick={(e) => {
									e.stopPropagation()
									onEdit(agent.id)
								}}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
								title="Edit agent"
							>
								<Edit className="h-4 w-4" />
							</button>
						)}
						{onLaunch && (
							<button
								type="button"
								onClick={(e) => {
									e.stopPropagation()
									onLaunch(agent.id)
								}}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
								title="Launch session"
							>
								<Play className="h-4 w-4" />
							</button>
						)}
						{agent.editable && onDelete && (
							<button
								type="button"
								onClick={(e) => {
									e.stopPropagation()
									onDelete(agent.id)
								}}
								className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-status-failed transition-colors"
								title="Delete agent"
							>
								<Trash2 className="h-4 w-4" />
							</button>
						)}
					</div>
				</div>
			))}
		</div>
	)
}
