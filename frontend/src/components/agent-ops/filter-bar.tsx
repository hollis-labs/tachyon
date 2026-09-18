import { FilterSearchInput } from "@hollis-labs/sysop-ui/data"
import { SlidersHorizontal } from "lucide-react"

// Provider-agnostic status — don't hardcode a provider's vocabulary.
// The filter bar derives chips dynamically from whatever statuses are
// actually present in the agent list.
export type AgentStatus = string

// Default colors for known statuses; unknown statuses fall back to neutral.
const STATUS_COLOR_MAP: Record<string, { bg: string; text: string; border: string }> = {
	active: { bg: "bg-status-done/10", text: "text-status-done", border: "border-status-done/40" },
	sleeping: { bg: "bg-panel-2/50", text: "text-text-subtle", border: "border-border" },
	enabled: { bg: "bg-status-done/10", text: "text-status-done", border: "border-status-done/40" },
	disabled: { bg: "bg-panel-2/50", text: "text-text-subtle", border: "border-border" },
}

const FALLBACK_COLORS = { bg: "bg-panel-2/50", text: "text-text-soft", border: "border-border" }

interface FilterBarProps {
	activeStatuses: AgentStatus[]
	onStatusToggle: (status: AgentStatus) => void
	searchQuery: string
	onSearchChange: (q: string) => void
	searchMatchCount?: number
	activeFilterCount?: number
	onClear?: () => void
	// Derived from the actual agent data — whatever statuses are present.
	availableStatuses: AgentStatus[]
}

export function FilterBar({
	activeStatuses,
	onStatusToggle,
	searchQuery,
	onSearchChange,
	searchMatchCount,
	activeFilterCount = 0,
	onClear,
	availableStatuses,
}: FilterBarProps) {
	return (
		<div className="flex flex-col gap-3 border-b border-border bg-panel-1 px-4 py-3">
			{/* Row 1: Status chips */}
			<div className="flex flex-wrap items-center gap-3 text-xs">
				<div className="flex flex-wrap items-center gap-1">
					<span className="mr-1 text-[10px] uppercase tracking-wider text-text-subtle">Status:</span>
					{availableStatuses.map((status) => {
						const active = activeStatuses.includes(status)
						const colors = STATUS_COLOR_MAP[status] || FALLBACK_COLORS
						return (
							<button
								key={status}
								type="button"
								onClick={() => onStatusToggle(status)}
								className={`rounded border px-2 py-0.5 text-[10px] uppercase tracking-wider transition-all ${
									active
										? `${colors.border} ${colors.bg} ${colors.text} ring-1 ring-white/20`
										: "border-border bg-panel-2/50 text-text-subtle opacity-50 hover:text-text-soft"
								}`}
							>
								{status}
							</button>
						)
					})}
				</div>
			</div>

			{/* Row 2: Search + summary */}
			<div className="flex items-center gap-3">
				<div className="flex-1">
					<FilterSearchInput
						value={searchQuery}
						onChange={onSearchChange}
						placeholder="Search agents by name, ID, or description..."
					/>
				</div>

				{/* Summary + clear */}
				<div className="flex items-center gap-2 text-xs text-text-subtle">
					{searchMatchCount !== undefined && (
						<div className="flex items-center gap-2 rounded border border-border bg-panel-2 px-2.5 py-1.5">
							<SlidersHorizontal className="h-3 w-3" />
							<span>
								{searchMatchCount} {searchMatchCount === 1 ? "match" : "matches"}
								{activeFilterCount > 0 && ` · ${activeFilterCount} ${activeFilterCount === 1 ? "filter" : "filters"}`}
							</span>
						</div>
					)}

					{onClear && activeFilterCount > 0 && (
						<button
							type="button"
							onClick={onClear}
							className="rounded px-2 py-1 text-xs text-text-subtle hover:bg-panel-2 hover:text-text"
						>
							Clear
						</button>
					)}
				</div>
			</div>
		</div>
	)
}
