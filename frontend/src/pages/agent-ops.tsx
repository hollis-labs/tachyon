import { useState, useEffect, useCallback, useMemo } from "react"
import { PageHeader, SummaryCards, Skeleton } from "@hollis-labs/sysop-ui"
import { FilterBar, type AgentStatus } from "../components/agent-ops/filter-bar"
import { AgentTable, type Agent } from "../components/agent-ops/agent-table"

const DEFAULT_ACTIVE_STATUSES: AgentStatus[] = ["running", "idle"]

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
	const [agents, setAgents] = useState<Agent[]>([])
	const [loading, setLoading] = useState(true)
	const [activeStatuses, setActiveStatuses] = useState<AgentStatus[]>(DEFAULT_ACTIVE_STATUSES)
	const [search, setSearch] = useState<string>("")

	// Mock data fetch
	useEffect(() => {
		const timer = setTimeout(() => {
			setAgents([
				{
					id: "agent-001",
					name: "CodeReviewer",
					status: "running",
					task: "Reviewing PR #1234",
					startedAt: "2m ago",
					duration: "2m 15s",
				},
				{
					id: "agent-002",
					name: "TestRunner",
					status: "completed",
					task: "Running test suite for feature-x",
					startedAt: "15m ago",
					duration: "3m 42s",
				},
				{
					id: "agent-003",
					name: "DocumentGenerator",
					status: "idle",
					task: "Waiting for next task",
					startedAt: "1h ago",
				},
				{
					id: "agent-004",
					name: "BugAnalyzer",
					status: "failed",
					task: "Analyzing issue #5678",
					startedAt: "5m ago",
					duration: "1m 8s",
				},
			])
			setLoading(false)
		}, 800)
		return () => clearTimeout(timer)
	}, [])

	function handleStatusToggle(status: AgentStatus) {
		setActiveStatuses((prev) =>
			prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
		)
	}

	function handleClearFilters() {
		setSearch("")
		setActiveStatuses(DEFAULT_ACTIVE_STATUSES)
	}

	function handleStatusChange(id: string, status: AgentStatus) {
		setAgents((prev) => prev.map((a) => (a.id === id ? { ...a, status } : a)))
	}

	function handleDelete(id: string) {
		setAgents((prev) => prev.filter((a) => a.id !== id))
	}

	const filteredAgents = useMemo(() => {
		let result = agents.filter((a) => activeStatuses.includes(a.status))
		if (search) {
			const query = search.toLowerCase()
			result = result.filter(
				(a) =>
					a.name.toLowerCase().includes(query) ||
					a.id.toLowerCase().includes(query) ||
					a.task.toLowerCase().includes(query)
			)
		}
		return result
	}, [agents, activeStatuses, search])

	const activeFilterCount =
		(activeStatuses.length !== DEFAULT_ACTIVE_STATUSES.length ? 1 : 0) + (search ? 1 : 0)

	const searchMatchCount = search ? filteredAgents.length : undefined
	const emptyVariant = activeFilterCount > 0 || search.length > 0 ? "no-results" : "no-agents"

	const runningCount = agents.filter((a) => a.status === "running").length
	const idleCount = agents.filter((a) => a.status === "idle").length
	const failedCount = agents.filter((a) => a.status === "failed").length
	const completedCount = agents.filter((a) => a.status === "completed").length

	const summaryCards = [
		{ label: "Running", value: runningCount, accentColor: "#60a5fa" },
		{ label: "Idle", value: idleCount },
		{ label: "Completed", value: completedCount, accentColor: "#34d399" },
		{ label: "Failed", value: failedCount, accentColor: "#f87171" },
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
						onStatusChange={handleStatusChange}
						onDelete={handleDelete}
						emptyVariant={emptyVariant}
					/>
				)}
			</div>
		</div>
	)
}
