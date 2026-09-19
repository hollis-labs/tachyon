import {
  Button,
  DetailSection,
  Input,
  Label,
  Pill,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from "@hollis-labs/sysop-ui"
import { type ReactNode, useEffect, useState } from "react"
import type { Agent, AgentCapabilities } from "../../api/client"
import { useApi } from "../../api/context"
import { AgentMCPPanel } from "./agent-mcp-panel"
import { AgentReflexesPanel } from "./agent-reflexes-panel"
import { AgentSkillsPanel } from "./agent-skills-panel"
import { AgentToolsPanel } from "./agent-tools-panel"
import { LargeDialog } from "./large-dialog"

// Nanite has no auth/operator-identity model exposed to Tachyon yet, so
// skill grants are attributed to a fixed operator name — the same
// "operator" convention Nanite's own reflex-create path already defaults
// to (internal/api/reflexes.go's handleCreateAgentReflex).
const GRANTED_BY = "operator"

// Tools and Skills are separate tabs, not stacked in one scrollable
// "Capabilities" tab: the tool checklist alone can run to hundreds of
// rows, and its own internal scroll captures wheel input long before a
// reader scrolls far enough to reach anything stacked below it.
type ManageTab = "overview" | "tools" | "skills" | "reflexes"

interface AgentManageDialogProps {
  agent: Agent | null
  /** The active provider's declared capabilities — combined with the row's own `editable` flag to decide whether Overview is an edit form or a read-only summary. Null while still loading; treated as permissive until it resolves false, to avoid a visible flicker for the common (Nanite, fully-capable) case. */
  capabilities?: AgentCapabilities | null
  onClose: () => void
  /** Called after a change that may affect the summary list (e.g. an edit or an MCP attach touching the agent row). */
  onAgentChanged: () => void
  /** Tab to land on when this agent opens — e.g. "tools" right after create, so a freshly created agent isn't left with zero grants. Defaults to "overview". */
  initialTab?: ManageTab
  /**
   * When set, renders this dialog as the continuation of the New Agent
   * wizard: a "Step 2 of 2" label plus a "Done" footer button (calling
   * onFinish) instead of the plain close-only chrome used when opening an
   * existing agent from the table.
   */
  stepLabel?: string
  onFinish?: () => void
}

export function AgentManageDialog({
  agent,
  capabilities,
  onClose,
  onAgentChanged,
  initialTab = "overview",
  stepLabel,
  onFinish,
}: AgentManageDialogProps) {
  const api = useApi()
  const [current, setCurrent] = useState<Agent | null>(agent)
  const [activeTab, setActiveTab] = useState<ManageTab>(initialTab)

  // Overview is the single edit surface for basic fields now — this is
  // "edit aligned with add new agent": same fields (name, can_execute,
  // system prompt, description), same dialog, just as a tab instead of a
  // separate small popup that had no path to Tools/Skills/Reflexes at all.
  const [overviewName, setOverviewName] = useState("")
  const [overviewSystemPrompt, setOverviewSystemPrompt] = useState("")
  const [overviewDescription, setOverviewDescription] = useState("")
  const [overviewCanExecute, setOverviewCanExecute] = useState(false)
  // Nanite "hides" an agent by setting status to "disabled"; anything else
  // counts as enabled.
  const [overviewEnabled, setOverviewEnabled] = useState(true)
  const [savingOverview, setSavingOverview] = useState(false)
  const [overviewError, setOverviewError] = useState<string | null>(null)

  useEffect(() => {
    setCurrent(agent)
    setActiveTab(initialTab)
    setOverviewName(agent?.name ?? "")
    setOverviewSystemPrompt(agent?.system_prompt ?? "")
    setOverviewDescription(agent?.description ?? "")
    setOverviewCanExecute(agent?.can_execute ?? false)
    setOverviewEnabled(agent?.status !== "disabled")
    setOverviewError(null)
  }, [agent, initialTab])

  async function refreshAgent() {
    if (!current) return
    try {
      const fresh = await api.getAgent(current.id)
      setCurrent(fresh)
    } catch {
      // The panel that triggered this already surfaced its own error toast.
    }
    onAgentChanged()
  }

  async function handleSaveOverview() {
    if (!current || !overviewName.trim() || !overviewSystemPrompt.trim()) return
    setSavingOverview(true)
    setOverviewError(null)
    try {
      const updated = await api.updateAgent(current.id, {
        name: overviewName.trim(),
        system_prompt: overviewSystemPrompt.trim(),
        description: overviewDescription.trim() || undefined,
        can_execute: overviewCanExecute,
        // Only when the toggle moved — an agent carrying some other status
        // value must not be rewritten to "active" by an unrelated edit.
        ...(overviewEnabled !== (current.status !== "disabled")
          ? { status: overviewEnabled ? "active" : "disabled" }
          : {}),
      })
      setCurrent(updated)
      onAgentChanged()
    } catch (error) {
      setOverviewError(error instanceof Error ? error.message : String(error))
    } finally {
      setSavingOverview(false)
    }
  }

  const overviewDirty =
    !!current &&
    (overviewName !== current.name ||
      overviewSystemPrompt !== (current.system_prompt ?? "") ||
      overviewDescription !== (current.description ?? "") ||
      overviewCanExecute !== current.can_execute ||
      overviewEnabled !== (current.status !== "disabled"))

  // Row-level editable (this specific agent, e.g. not external/internal)
  // AND provider-level can_update (this adapter supports editing at all).
  // capabilities?.can_update !== false stays permissive while the
  // declaration is still loading (null) rather than flashing read-only
  // then editable for the common fully-capable case.
  const canEditOverview = !!current?.editable && capabilities?.can_update !== false

  const footerButtons: ReactNode[] = []
  if (canEditOverview && activeTab === "overview") {
    footerButtons.push(
      <Button
        key="save"
        type="button"
        size="sm"
        variant={onFinish ? "outline" : "default"}
        onClick={handleSaveOverview}
        disabled={
          !overviewDirty || !overviewName.trim() || !overviewSystemPrompt.trim() || savingOverview
        }
      >
        {savingOverview ? "Saving…" : "Save Changes"}
      </Button>,
    )
  }
  if (onFinish) {
    footerButtons.push(
      <Button key="done" type="button" size="sm" onClick={onFinish}>
        Done
      </Button>,
    )
  }

  return (
    <LargeDialog
      open={!!current}
      onClose={onClose}
      title={current?.name || ""}
      meta={
        <div className="flex items-center gap-3 text-xs text-text-subtle">
          <span>ID: {current?.id}</span>
          {current?.slug && <span>Slug: {current.slug}</span>}
          {current && (
            <Pill tone={current.can_execute ? "success" : "neutral"}>
              {current.can_execute ? "Can execute" : "Text only"}
            </Pill>
          )}
        </div>
      }
      footer={footerButtons.length > 0 ? <>{footerButtons}</> : undefined}
    >
      {current && (
        <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as ManageTab)}>
          {stepLabel && (
            <p className="px-4 pt-3 text-xs font-medium uppercase tracking-wider text-text-subtle">
              {stepLabel}
            </p>
          )}
          <TabsList className="mx-4 mt-1">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="tools">Tools</TabsTrigger>
            <TabsTrigger value="skills">Skills</TabsTrigger>
            <TabsTrigger value="reflexes">Reflexes</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="px-4 py-3">
            {canEditOverview ? (
              <div className="flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="overview-name">Name</Label>
                  <Input
                    id="overview-name"
                    value={overviewName}
                    onChange={(e) => setOverviewName(e.target.value)}
                  />
                </div>
                <div className="flex items-center gap-2">
                  <Switch checked={overviewCanExecute} onCheckedChange={setOverviewCanExecute} />
                  <Label>Can execute (spawnable as a subagent worker)</Label>
                </div>
                <div className="flex items-center gap-2">
                  <Switch checked={overviewEnabled} onCheckedChange={setOverviewEnabled} />
                  <Label>Enabled</Label>
                  <span className="text-xs text-text-subtle">
                    Disabled agents are hidden from Nanite's agent list and chat pickers.
                  </span>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="overview-system-prompt">System prompt</Label>
                  <Textarea
                    id="overview-system-prompt"
                    value={overviewSystemPrompt}
                    onChange={(e) => setOverviewSystemPrompt(e.target.value)}
                    rows={10}
                    className="font-mono text-xs"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="overview-description">Description</Label>
                  <Textarea
                    id="overview-description"
                    value={overviewDescription}
                    onChange={(e) => setOverviewDescription(e.target.value)}
                    rows={3}
                  />
                </div>
                <div className="flex flex-wrap items-center gap-3 text-xs text-text-subtle">
                  {current.layer && <span>Layer: {current.layer}</span>}
                  {current.tags && <span>Tags: {current.tags}</span>}
                </div>
                {overviewError && <p className="text-xs text-status-failed">{overviewError}</p>}
              </div>
            ) : (
              <div className="flex flex-col gap-4">
                <DetailSection title="Status">
                  <div className="flex items-center gap-2">
                    <div
                      className={`h-2 w-2 rounded-full ${current.status === "active" ? "bg-status-done" : "bg-text-subtle"}`}
                    />
                    <span className="text-sm capitalize">{current.status}</span>
                    <span className="ml-2 text-xs text-text-subtle bg-panel-2 px-1.5 py-0.5 rounded">
                      Not editable ({current.layer || "external"})
                    </span>
                  </div>
                </DetailSection>

                {current.description && (
                  <DetailSection title="Description">
                    <p className="text-sm text-text-soft">{current.description}</p>
                  </DetailSection>
                )}

                {current.system_prompt && (
                  <DetailSection title="System Prompt">
                    <pre className="text-xs text-text-soft whitespace-pre-wrap font-mono bg-panel-2 p-3 rounded border border-border">
                      {current.system_prompt}
                    </pre>
                  </DetailSection>
                )}

                {current.tags && (
                  <DetailSection title="Tags">
                    <p className="text-sm text-text-soft">{current.tags}</p>
                  </DetailSection>
                )}
              </div>
            )}
          </TabsContent>

          <TabsContent value="tools" className="px-4 py-3">
            <div className="flex flex-col gap-6">
              <div>
                <h4 className="text-xs font-medium text-text-subtle mb-2">Tool grants</h4>
                <AgentToolsPanel agentId={current.id} />
              </div>
              <div>
                <h4 className="text-xs font-medium text-text-subtle mb-2">MCP servers</h4>
                <AgentMCPPanel
                  agentId={current.id}
                  mcpServersJson={current.mcp_servers}
                  onChanged={refreshAgent}
                />
              </div>
            </div>
          </TabsContent>

          <TabsContent value="skills" className="px-4 py-3">
            <AgentSkillsPanel agentId={current.id} grantedBy={GRANTED_BY} />
          </TabsContent>

          <TabsContent value="reflexes" className="px-4 py-3">
            <AgentReflexesPanel agentId={current.id} />
          </TabsContent>
        </Tabs>
      )}
    </LargeDialog>
  )
}
