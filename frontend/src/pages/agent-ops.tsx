import { useState, useEffect, useCallback, useMemo } from "react"
import {
	PageHeader,
	SummaryCards,
	Skeleton,
	FormDialog,
	Button,
	Input,
	Label,
	Textarea,
} from "@hollis-labs/sysop-ui"
import { Plus } from "lucide-react"
import { FilterBar, type AgentStatus } from "../components/agent-ops/filter-bar"
import { AgentTable, type Agent } from "../components/agent-ops/agent-table"
import { useApi } from "../api/context"

const DEFAULT_ACTIVE_STATUSES: AgentStatus[] = ["active", "sleeping"]

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

	const [createOpen, setCreateOpen] = useState(false)
	const [createName, setCreateName] = useState("")
	const [createSystemPrompt, setCreateSystemPrompt] = useState("")
	const [createDescription, setCreateDescription] = useState("")
	const [creating, setCreating] = useState(false)
	const [createError, setCreateError] = useState<string | null>(null)

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

	function openCreateDialog() {
		setCreateName("")
		setCreateSystemPrompt("")
		setCreateDescription("")
		setCreateError(null)
		setCreateOpen(true)
	}

	async function handleCreateSubmit() {
		if (!createName.trim() || !createSystemPrompt.trim()) return
		setCreating(true)
		setCreateError(null)
		try {
			await api.createAgent({
				name: createName.trim(),
				system_prompt: createSystemPrompt.trim(),
				agent_prompt: createDescription.trim() || undefined,
			})
			setCreateOpen(false)
			await loadAgents()
		} catch (error) {
			setCreateError(error instanceof Error ? error.message : String(error))
		} finally {
			setCreating(false)
		}
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
	const activeCount = agents.filter((a) => a.status === "active").length
	const sleepingCount = agents.filter((a) => a.status === "sleeping").length
	const editableCount = agents.filter((a) => a.editable).length

	const summaryCards = [
		{ label: "Total Agents", value: totalCount },
		{ label: "Active", value: activeCount, accentColor: "#34d399" },
		{ label: "Sleeping", value: sleepingCount },
		{ label: "Editable", value: editableCount, accentColor: "#60a5fa" },
	]

	return (
		<div className="flex h-full flex-col">
			<PageHeader title="Agent Operations">
				<Button type="button" size="sm" onClick={openCreateDialog}>
					<Plus className="h-4 w-4" />
					New Agent
				</Button>
			</PageHeader>
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
			<FormDialog
				open={createOpen}
				onClose={() => setCreateOpen(false)}
				title="New Agent"
				description="Create a new agent profile in Nanite."
				onSubmit={handleCreateSubmit}
				submitLabel="Create Agent"
				submitDisabled={!createName.trim() || !createSystemPrompt.trim()}
				submitting={creating}
			>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="agent-name">Name</Label>
					<Input
						id="agent-name"
						value={createName}
						onChange={(e) => setCreateName(e.target.value)}
						placeholder="e.g. Release Notes Writer"
						autoFocus
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="agent-system-prompt">System prompt</Label>
					<Textarea
						id="agent-system-prompt"
						value={createSystemPrompt}
						onChange={(e) => setCreateSystemPrompt(e.target.value)}
						placeholder="You are..."
						rows={6}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="agent-description">Description (optional)</Label>
					<Textarea
						id="agent-description"
						value={createDescription}
						onChange={(e) => setCreateDescription(e.target.value)}
						placeholder="What this agent is for"
						rows={2}
					/>
				</div>
				{createError && (
					<p className="text-xs text-status-failed">{createError}</p>
				)}
			</FormDialog>
		</div>
	)
}
