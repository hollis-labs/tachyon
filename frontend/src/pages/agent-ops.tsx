import {
  Button,
  Input,
  Label,
  notifyError,
  notifySuccess,
  PageHeader,
  Skeleton,
  SummaryCards,
  Switch,
  Textarea,
} from "@hollis-labs/sysop-ui"
import { Plus } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { AgentCapabilities, Agent as ApiAgent } from "../api/client"
import { useApi } from "../api/context"
import { AgentManageDialog } from "../components/agent-ops/agent-manage-dialog"
import { type Agent, AgentTable } from "../components/agent-ops/agent-table"
import { type AgentStatus, FilterBar } from "../components/agent-ops/filter-bar"
import { LargeDialog } from "../components/agent-ops/large-dialog"

const HIDDEN_BY_DEFAULT_STATUS = "disabled"

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
  const [activeStatuses, setActiveStatuses] = useState<AgentStatus[]>([])
  const [search, setSearch] = useState<string>("")
  // What the active provider actually supports — checked before offering
  // "New Agent" at all, instead of assuming every provider can author
  // agents the way Nanite does. Defaults closed (all false) until the
  // real declaration loads, so the button doesn't flash on then off.
  const [capabilities, setCapabilities] = useState<AgentCapabilities | null>(null)

  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState("")
  const [createSystemPrompt, setCreateSystemPrompt] = useState("")
  const [createDescription, setCreateDescription] = useState("")
  const [createCanExecute, setCreateCanExecute] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const [detailAgent, setDetailAgent] = useState<ApiAgent | null>(null)
  const [detailInitialTab, setDetailInitialTab] = useState<
    "overview" | "tools" | "skills" | "reflexes"
  >("overview")
  // True only while the manage dialog is showing as "step 2" of the New
  // Agent wizard (i.e. opened right after a create, not via a row click) —
  // drives the step label and the "Done" footer button.
  const [creationWizardActive, setCreationWizardActive] = useState(false)

  function closeDetailDialog() {
    setDetailAgent(null)
    setCreationWizardActive(false)
  }

  // Fetch agents from API
  const loadAgents = useCallback(async () => {
    try {
      setLoading(true)
      const result = await api.listAgents()
      setAgents(
        result.map((a) => ({
          id: a.id,
          name: a.name,
          slug: a.slug,
          description: a.description,
          tags: a.tags,
          icon: a.icon,
          status: a.status || "unknown",
          layer: a.layer,
          editable: a.editable,
        })),
      )
    } catch (error) {
      console.error("Failed to load agents:", error)
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    loadAgents()
  }, [loadAgents])

  useEffect(() => {
    api
      .getCapabilities()
      .then(setCapabilities)
      .catch((error) => console.error("Failed to load provider capabilities:", error))
  }, [api])

  // Derive available statuses from actual agent data (provider-agnostic)
  const availableStatuses = useMemo(() => {
    const statuses = Array.from(new Set(agents.map((a) => a.status)))
    return statuses.sort()
  }, [agents])

  // Nanite hides "disabled" agents unless asked ("Show disabled" in its
  // Admin list). Keep that default so disabling an agent hides it the same
  // way in-session and after a reload; its status chip is the "show" control.
  const defaultStatuses = useMemo(
    () => availableStatuses.filter((s) => s !== HIDDEN_BY_DEFAULT_STATUS),
    [availableStatuses],
  )

  // Initialize active statuses to the default (non-hidden) set on first load
  useEffect(() => {
    if (agents.length > 0 && activeStatuses.length === 0) {
      setActiveStatuses(defaultStatuses)
    }
  }, [agents, defaultStatuses, activeStatuses.length])

  function handleStatusToggle(status: AgentStatus) {
    setActiveStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status],
    )
  }

  function handleClearFilters() {
    setSearch("")
    setActiveStatuses(defaultStatuses)
  }

  // Opens the same tabbed manage dialog for both a row click and the
  // pencil "edit" icon — edit aligned with add new agent means one entry
  // point (Overview tab, now itself editable) instead of a separate small
  // popup with no path to Tools/Skills/Reflexes.
  async function openManageDialog(id: string) {
    try {
      const fullAgent = await api.getAgent(id)
      setDetailInitialTab("overview")
      setCreationWizardActive(false)
      setDetailAgent(fullAgent)
    } catch (error) {
      console.error("Failed to fetch agent details:", error)
      alert("Failed to load agent details")
    }
  }

  function handleRowClick(agent: Agent) {
    openManageDialog(agent.id)
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
    try {
      const { session_id } = await api.launchAgent(id)
      notifySuccess(`Session launched: ${session_id}`)
    } catch (error) {
      notifyError(error, "Failed to launch session")
    }
  }

  function openCreateDialog() {
    setCreateName("")
    setCreateSystemPrompt("")
    setCreateDescription("")
    setCreateCanExecute(false)
    setCreateError(null)
    setCreateOpen(true)
  }

  async function handleCreateSubmit() {
    if (!createName.trim() || !createSystemPrompt.trim()) return
    setCreating(true)
    setCreateError(null)
    try {
      const created = await api.createAgent({
        name: createName.trim(),
        system_prompt: createSystemPrompt.trim(),
        description: createDescription.trim() || undefined,
        can_execute: createCanExecute,
      })
      setCreateOpen(false)
      await loadAgents()
      // Step 2 of 2: land straight on Tools so a freshly created agent
      // doesn't sit at zero tool grants with no obvious next step.
      setDetailInitialTab("tools")
      setCreationWizardActive(true)
      setDetailAgent(created)
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
          (a.tags && a.tags.toLowerCase().includes(query)),
      )
    }
    return result
  }, [agents, activeStatuses, search])

  // A status filter counts as "active" only when it departs from the default
  // view (a default status switched off, or a hidden one switched on).
  const statusFilterActive =
    defaultStatuses.some((s) => !activeStatuses.includes(s)) ||
    activeStatuses.some((s) => availableStatuses.includes(s) && !defaultStatuses.includes(s))
  const activeFilterCount = (statusFilterActive ? 1 : 0) + (search ? 1 : 0)

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
        {capabilities?.can_create && (
          <Button type="button" size="sm" onClick={openCreateDialog}>
            <Plus className="h-4 w-4" />
            New Agent
          </Button>
        )}
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
        availableStatuses={availableStatuses}
      />
      <div className="flex-1 overflow-auto">
        {loading ? (
          <TableSkeleton />
        ) : (
          <AgentTable
            agents={filteredAgents}
            onEdit={capabilities?.can_update ? openManageDialog : undefined}
            onDelete={capabilities?.can_delete ? handleDelete : undefined}
            onLaunch={handleLaunch}
            onRowClick={handleRowClick}
            emptyVariant={emptyVariant}
          />
        )}
      </div>
      <LargeDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="New Agent"
        description="Step 1 of 2 — Basic Info. Capabilities (tools, skills, MCP servers, reflexes) come next."
        footer={
          <>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setCreateOpen(false)}
              disabled={creating}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              form="create-agent-form"
              disabled={!createName.trim() || !createSystemPrompt.trim() || creating}
            >
              Next
            </Button>
          </>
        }
      >
        <form
          id="create-agent-form"
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            handleCreateSubmit()
          }}
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
          <div className="flex items-center gap-2">
            <Switch checked={createCanExecute} onCheckedChange={setCreateCanExecute} />
            <Label>Can execute (spawnable as a subagent worker)</Label>
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
          {createError && <p className="text-xs text-status-failed">{createError}</p>}
        </form>
      </LargeDialog>

      <AgentManageDialog
        agent={detailAgent}
        capabilities={capabilities}
        initialTab={detailInitialTab}
        onClose={closeDetailDialog}
        onAgentChanged={loadAgents}
        stepLabel={creationWizardActive ? "Step 2 of 2 — Tools, Skills & Reflexes" : undefined}
        onFinish={creationWizardActive ? closeDetailDialog : undefined}
      />
    </div>
  )
}
