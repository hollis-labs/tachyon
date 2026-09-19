import {
  Button,
  ConfirmDialog,
  Input,
  Label,
  notifyError,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Skeleton,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Textarea,
} from "@hollis-labs/sysop-ui"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import type { CreateReflexRequest, Reflex } from "../../api/client"
import { useApi } from "../../api/context"

interface AgentReflexesPanelProps {
  agentId: string
}

const emptyForm: CreateReflexRequest = {
  name: "",
  trigger_kind: "",
  trigger_spec: "{}",
  action_kind: "",
  action_spec: "{}",
  priority: 0,
  opt_out_allowed: true,
  recurrence_override_seconds: null,
}

// Nanite's validateReflexDefinition (internal/api/reflexes.go) rejects
// anything outside these two fixed enums (internal/store/agent_reflexes.go).
const TRIGGER_KINDS = [
  { value: "predicate", label: "Predicate" },
  { value: "event", label: "Event" },
  { value: "interval", label: "Interval" },
]
const ACTION_KINDS = [
  { value: "inject_reminder", label: "Inject reminder" },
  { value: "force_tool_choice", label: "Force tool choice" },
  { value: "send_message", label: "Send message" },
  { value: "halt_session", label: "Halt session" },
  { value: "add_schedule", label: "Add schedule" },
  { value: "dispatch_to_agent", label: "Dispatch to agent" },
  { value: "resume_loop_run", label: "Resume loop run" },
]

export function AgentReflexesPanel({ agentId }: AgentReflexesPanelProps) {
  const api = useApi()
  const [reflexes, setReflexes] = useState<Reflex[]>([])
  const [loading, setLoading] = useState(true)

  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Reflex | null>(null)
  const [form, setForm] = useState<CreateReflexRequest>(emptyForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const [deleteTarget, setDeleteTarget] = useState<Reflex | null>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(async () => {
    try {
      setLoading(true)
      setReflexes(await api.listAgentReflexes(agentId))
    } catch (error) {
      notifyError(error, "Failed to load reflexes")
    } finally {
      setLoading(false)
    }
  }, [api, agentId])

  useEffect(() => {
    load()
  }, [load])

  function openCreate() {
    setEditing(null)
    setForm(emptyForm)
    setFormError(null)
    setFormOpen(true)
  }

  function openEdit(reflex: Reflex) {
    setEditing(reflex)
    setForm({
      name: reflex.name,
      trigger_kind: reflex.trigger_kind,
      trigger_spec: reflex.trigger_spec,
      action_kind: reflex.action_kind,
      action_spec: reflex.action_spec,
      priority: reflex.priority,
      opt_out_allowed: reflex.opt_out_allowed,
      recurrence_override_seconds: reflex.recurrence_override_seconds ?? null,
    })
    setFormError(null)
    setFormOpen(true)
  }

  async function handleSubmit() {
    setSaving(true)
    setFormError(null)
    try {
      if (editing) {
        await api.updateAgentReflex(agentId, editing.id, form)
      } else {
        await api.createAgentReflex(agentId, form)
      }
      setFormOpen(false)
      await load()
    } catch (error) {
      setFormError(error instanceof Error ? error.message : String(error))
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.deleteAgentReflex(agentId, deleteTarget.id)
      setDeleteTarget(null)
      await load()
    } catch (error) {
      notifyError(error, "Failed to delete reflex")
    } finally {
      setDeleting(false)
    }
  }

  const canSubmit = form.name.trim() && form.trigger_kind.trim() && form.action_kind.trim()

  // GET /api/agents/{id}/reflexes returns two different kinds of row in
  // one list: reflexes this agent actually owns (agent_id === agentId,
  // fully editable/deletable here) and reflexes inherited from its class
  // (agent_id is empty — a global reflex like "idle_drift_advisor" that
  // auto-applies to every agent of that class). Nanite rejects an
  // edit/delete against the latter ("cannot patch/delete inherited or
  // different-agent reflex"), and — per internal/store/agent_reflexes.go's
  // ListAgentReflexesForAgent — has no API route at all yet for detaching
  // one (the agent_reflex_opt_outs table it reads exists, but nothing
  // writes to it). So inherited rows render read-only with no action
  // buttons, rather than offering an edit/delete that would just 400.
  const ownReflexes = reflexes.filter((r) => r.agent_id === agentId)
  const inheritedReflexes = reflexes.filter((r) => r.agent_id !== agentId)

  if (loading) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full rounded-md" />
        ))}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        {!formOpen && (
          <Button type="button" size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4" />
            New Reflex
          </Button>
        )}
      </div>

      {/*
        Inline, not a modal: this used to be a FormDialog stacked on top of
        the already-open agent manage dialog — a Dialog nested two levels
        deep inside another Dialog, with a Select's popover a third level
        in. Reflex creation reportedly didn't work; an inline section here
        removes that nesting as a variable entirely rather than debugging
        exactly how deep it was the cause.
      */}
      {formOpen && (
        <div className="flex flex-col gap-3 rounded-lg border border-border bg-panel/40 p-3">
          <h4 className="text-xs font-medium text-text-subtle">
            {editing ? "Edit Reflex" : "New Reflex"}
          </h4>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reflex-name">Name</Label>
            <Input
              id="reflex-name"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              autoFocus
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="reflex-trigger-kind">Trigger kind</Label>
              <Select
                value={form.trigger_kind || null}
                onValueChange={(value) =>
                  setForm((f) => ({ ...f, trigger_kind: (value as string) ?? "" }))
                }
              >
                <SelectTrigger id="reflex-trigger-kind">
                  <SelectValue placeholder="Select a trigger kind" />
                </SelectTrigger>
                <SelectContent>
                  {TRIGGER_KINDS.map((k) => (
                    <SelectItem key={k.value} value={k.value}>
                      {k.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="reflex-action-kind">Action kind</Label>
              <Select
                value={form.action_kind || null}
                onValueChange={(value) =>
                  setForm((f) => ({ ...f, action_kind: (value as string) ?? "" }))
                }
              >
                <SelectTrigger id="reflex-action-kind">
                  <SelectValue placeholder="Select an action kind" />
                </SelectTrigger>
                <SelectContent>
                  {ACTION_KINDS.map((k) => (
                    <SelectItem key={k.value} value={k.value}>
                      {k.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reflex-trigger-spec">Trigger spec (JSON)</Label>
            <Textarea
              id="reflex-trigger-spec"
              value={form.trigger_spec}
              onChange={(e) => setForm((f) => ({ ...f, trigger_spec: e.target.value }))}
              rows={2}
              className="font-mono text-xs"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="reflex-action-spec">Action spec (JSON)</Label>
            <Textarea
              id="reflex-action-spec"
              value={form.action_spec}
              onChange={(e) => setForm((f) => ({ ...f, action_spec: e.target.value }))}
              rows={2}
              className="font-mono text-xs"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="reflex-priority">Priority</Label>
              <Input
                id="reflex-priority"
                type="number"
                value={form.priority ?? 0}
                onChange={(e) => setForm((f) => ({ ...f, priority: Number(e.target.value) }))}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="reflex-recurrence">Recurrence override (seconds)</Label>
              <Input
                id="reflex-recurrence"
                type="number"
                value={form.recurrence_override_seconds ?? ""}
                placeholder="inherit default"
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    recurrence_override_seconds:
                      e.target.value === "" ? null : Number(e.target.value),
                  }))
                }
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Switch
              checked={form.opt_out_allowed ?? true}
              onCheckedChange={(checked) => setForm((f) => ({ ...f, opt_out_allowed: checked }))}
            />
            <Label>Agent may opt out</Label>
          </div>
          {formError && <p className="text-xs text-status-failed">{formError}</p>}
          <div className="flex justify-end gap-2 border-t border-border pt-3">
            <Button
              type="button"
              variant="ghost"
              onClick={() => setFormOpen(false)}
              disabled={saving}
            >
              Cancel
            </Button>
            <Button type="button" onClick={handleSubmit} disabled={!canSubmit || saving}>
              {editing ? "Save Changes" : "Create Reflex"}
            </Button>
          </div>
        </div>
      )}

      <div>
        <h4 className="text-xs font-medium text-text-subtle mb-2">This agent's reflexes</h4>
        {ownReflexes.length === 0 ? (
          <p className="text-sm text-text-subtle">
            No reflexes defined on this agent yet — use "New Reflex" above to add one.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Trigger</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Priority</TableHead>
                <TableHead>Opt-out</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ownReflexes.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="font-medium">{r.name}</TableCell>
                  <TableCell className="text-xs text-text-subtle">
                    {r.trigger_kind}
                    {r.recurrence_override_seconds
                      ? ` · every ${r.recurrence_override_seconds}s`
                      : ""}
                  </TableCell>
                  <TableCell className="text-xs text-text-subtle">{r.action_kind}</TableCell>
                  <TableCell>{r.priority}</TableCell>
                  <TableCell>{r.opt_out_allowed ? "Allowed" : "Locked"}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <button
                        type="button"
                        onClick={() => openEdit(r)}
                        className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-text transition-colors"
                        title="Edit reflex"
                      >
                        <Pencil className="h-4 w-4" />
                      </button>
                      <button
                        type="button"
                        onClick={() => setDeleteTarget(r)}
                        className="p-1.5 rounded hover:bg-panel-2 text-text-subtle hover:text-status-failed transition-colors"
                        title="Delete reflex"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      {inheritedReflexes.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-text-subtle mb-2">Inherited from agent class</h4>
          <p className="mb-2 text-xs text-text-subtle">
            These apply to every agent of this class automatically. Nanite doesn't yet expose a way
            to detach one from a single agent — read-only here for now.
          </p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Trigger</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Priority</TableHead>
                <TableHead>Opt-out</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {inheritedReflexes.map((r) => (
                <TableRow key={r.id} className="opacity-70">
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {r.name}
                      <Pill tone="neutral">Inherited</Pill>
                    </div>
                  </TableCell>
                  <TableCell className="text-xs text-text-subtle">
                    {r.trigger_kind}
                    {r.recurrence_override_seconds
                      ? ` · every ${r.recurrence_override_seconds}s`
                      : ""}
                  </TableCell>
                  <TableCell className="text-xs text-text-subtle">{r.action_kind}</TableCell>
                  <TableCell>{r.priority}</TableCell>
                  <TableCell>{r.opt_out_allowed ? "Allowed" : "Locked"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="Delete reflex"
        description={
          deleteTarget ? `Delete "${deleteTarget.name}"? This cannot be undone.` : undefined
        }
        onConfirm={handleDelete}
        confirmLabel="Delete"
        busy={deleting}
      />
    </div>
  )
}
