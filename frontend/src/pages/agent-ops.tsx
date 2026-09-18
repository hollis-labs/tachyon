import { useState, useEffect, useCallback, useMemo } from "react"
import { PageHeader, SummaryCards, Skeleton } from "@hollis-labs/sysop-ui"
import { FilterBar, type AgentStatus } from "../components/agent-ops/filter-bar"
import { AgentTable, type Agent } from "../components/agent-ops/agent-table"
import { useApi } from "../api/context"

const DEFAULT_ACTIVE_STATUSES: AgentStatus[] = ["enabled", "disabled"]

function TableSkeleton() {
	return (
		<div className="flex flex-col gap-2 p-4">
			{Array.from({ length: 6 }).map((_, i) => (
				<Skeleton key={i} className="h-16 w-full rounded-md" />
			))}
		</div>
	)
}

export function AgentOpsPage() {
	const api = useApi()
	const [agents, setAgents] = useState<Agent[]>([])
	const [loading, setLoading] = useState(true)
	const [activeStatuses, setActiveStatuses] = useState<AgentStatus[]>(DEFAULT_ACTIVE_STATUSES)
	const [search, setSearch] = useState<string>("")

	// Fetch agents from API
	const loadAgents = useCallback(async () => {
		try {
			setLoading(true)
			const result = await api.listAgents()
			setAgents(result.map(a => ({
				id: a.id,
				name: a.name,
				slug: a.slug,
				description: a.description,
				tags: a.tags,
				icon: a.icon,
				status: (a.status || "enabled") as AgentStatus,
				layer: a.layer,
				editable: a.editable,
			})))
		} catch (error) {
			console.error("Failed to load agents:", error)
		} finally {
			setLoading(false)
		}
	}, [api])

	useEffect(() => {
		loadAgents()
	}, [loadAgents])

	function handleStatusToggle(status: AgentStatus) {
		setActiveStatuses((prev) =>
			prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
		)
	}

	function handleClearFilters() {
		setSearch("")
		setActiveStatuses(DEFAULT_ACTIVE_STATUSES)
	}

	async function handleEdit(id: string) {
		// TODO: Open edit modal
		console.log("Edit agent:", id)
	}

	async function handleDelete(id: string) {
		if (!confirm("Are you sure you want to delete this agent?")) return
		try {
			await api.deleteAgent(id)
			await loadAgents()
		} catch (error) {
			console.error("Failed to delete agent:", error)
			alert("Failed to delete agent")
		}
	}

	async function handleLaunch(id: string) {
		// TODO: Launch session for this agent
		console.log("Launch session for agent:", id)
	}

	const filteredAgents = useMemo(() => {
		let result = agents.filter((a) => activeStatuses.includes(a.status))
		if (search) {
			const query = search.toLowerCase()
			result = result.filter(
				(a) =>
					a.name.toLowerCase().includes(query) ||
					a.id.toLowerCase().includes(query) ||
					(a.description && a.description.toLowerCase().includes(query)) ||
					(a.slug && a.slug.toLowerCase().includes(query)) ||
					(a.tags && a.tags.toLowerCase().includes(query))
			)
		}
		return result
	}, [agents, activeStatuses, search])

	const activeFilterCount =
		(activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ? 1 : 0) + (search ? 1 : 0)

	const searchMatchCount = search ? filteredAgents.length : undefined
	const emptyVariant = activeFilterCount > 0 || search.length > 0 ? "no-results" : "no-agents"

	const totalCount = agents.length
	const enabledCount = agents.filter((a) => a.status === "enabled").length
	const disabledCount = agents.filter((a) => a.status === "disabled").length
	const editableCount = agents.filter((a) => a.editable).length

	const summaryCards = [
		{ label: "Total Agents", value: totalCount },
		{ label: "Enabled", value: enabledCount, accentColor: "#34d399" },
		{ label: "Disabled", value: disabledCount },
		{ label: "Editable", value: editableCount, accentColor: "#60a5fa" },
	]

	return (
		<div className="flex h-full flex-col">
			<PageHeader title="Agent Operations" />
			{!loading && <SummaryCards cards={summaryCards} />}
			<FilterBar
				activeStatuses={activeStatuses}
				onStatusToggle={handleStatusToggle}
				searchQuery={search}
				onSearchChange={setSearch}
				searchMatchCount={searchMatchCount}
				activeFilterCount={activeFilterCount}
				onClear={handleClearFilters}
			/>
			<div className="flex-1 overflow-auto">
				{loading ? (
					<TableSkeleton />
				) : (
					<AgentTable
						agents={filteredAgents}
						onEdit={handleEdit}
						onDelete={handleDelete}
						onLaunch={handleLaunch}
						emptyVariant={emptyVariant}
					/>
				)}
			</div>
		</div>
	)
}
