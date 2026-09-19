import {
  Button,
  notifyError,
  Pill,
  Skeleton,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { AgentSkill, Skill, SkillGrantStatus } from "../../api/client"
import { useApi } from "../../api/context"
import { type ChecklistItem, SearchableChecklist } from "./checklist"

interface AgentSkillsPanelProps {
  agentId: string
  grantedBy: string
}

const STATUS_TONE: Record<SkillGrantStatus["status"], "success" | "warning" | "danger"> = {
  approved: "success",
  grant_required: "warning",
  reapproval_required: "danger",
}

const STATUS_LABEL: Record<SkillGrantStatus["status"], string> = {
  approved: "Approved",
  grant_required: "Needs approval",
  reapproval_required: "Needs re-approval",
}

export function AgentSkillsPanel({ agentId, grantedBy }: AgentSkillsPanelProps) {
  const api = useApi()
  const [catalog, setCatalog] = useState<Skill[]>([])
  const [assigned, setAssigned] = useState<AgentSkill[]>([])
  const [grants, setGrants] = useState<Record<string, SkillGrantStatus>>({})
  const [loading, setLoading] = useState(true)
  const [pendingSlug, setPendingSlug] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setLoading(true)
      const [catalogResult, assignedResult] = await Promise.all([
        api.listSkillCatalog(),
        api.listAgentSkills(agentId),
      ])
      setCatalog(catalogResult)
      setAssigned(assignedResult)
      const statuses = await Promise.all(
        assignedResult.map((s) => api.getAgentSkillGrant(agentId, s.skill_slug).catch(() => null)),
      )
      const next: Record<string, SkillGrantStatus> = {}
      statuses.forEach((status) => {
        if (status) next[status.skill_slug] = status
      })
      setGrants(next)
    } catch (error) {
      notifyError(error, "Failed to load skills")
    } finally {
      setLoading(false)
    }
  }, [api, agentId])

  useEffect(() => {
    load()
  }, [load])

  const slugToId = useMemo(() => new Map(catalog.map((s) => [s.slug, s.id])), [catalog])

  const items: ChecklistItem[] = useMemo(
    () =>
      catalog.map((s) => ({
        value: s.id,
        label: s.name,
        description: s.description,
        category: s.category || undefined,
      })),
    [catalog],
  )
  const checkedIds = useMemo(
    () =>
      new Set(assigned.map((a) => slugToId.get(a.skill_slug)).filter((id): id is string => !!id)),
    [assigned, slugToId],
  )
  const [pendingAssignId, setPendingAssignId] = useState<string | null>(null)

  async function handleAssignToggle(skillId: string, next: boolean) {
    setPendingAssignId(skillId)
    try {
      if (next) {
        await api.assignAgentSkill(agentId, skillId)
      } else {
        await api.removeAgentSkill(agentId, skillId)
      }
    } catch (error) {
      notifyError(error, next ? "Failed to assign skill" : "Failed to remove skill")
    } finally {
      await load()
      setPendingAssignId(null)
    }
  }

  async function handleGrant(slug: string) {
    setPendingSlug(slug)
    try {
      await api.grantAgentSkill(agentId, slug, grantedBy)
    } catch (error) {
      notifyError(error, "Failed to approve skill")
    } finally {
      await load()
      setPendingSlug(null)
    }
  }

  async function handleRevoke(slug: string) {
    setPendingSlug(slug)
    try {
      await api.revokeAgentSkillGrant(agentId, slug)
    } catch (error) {
      notifyError(error, "Failed to revoke skill approval")
    } finally {
      await load()
      setPendingSlug(null)
    }
  }

  if (loading) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full rounded-md" />
        ))}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <SearchableChecklist
        items={items}
        checked={checkedIds}
        onToggle={handleAssignToggle}
        searchPlaceholder="Search skills by name or description…"
        emptyText="No skills match"
        categoryLabel="categories"
      />
      {pendingAssignId && <p className="text-xs text-text-subtle">Updating assignment…</p>}

      {assigned.length > 0 && (
        <div>
          <h4 className="text-xs font-medium text-text-subtle mb-2">Execution approval</h4>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Skill</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {assigned.map((a) => {
                const status = grants[a.skill_slug]
                const busy = pendingSlug === a.skill_slug
                return (
                  <TableRow key={a.skill_slug}>
                    <TableCell className="font-medium">{a.name || a.skill_slug}</TableCell>
                    <TableCell>
                      {status ? (
                        <Pill tone={STATUS_TONE[status.status]} dot>
                          {STATUS_LABEL[status.status]}
                        </Pill>
                      ) : (
                        <Pill tone="neutral">Unknown</Pill>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {status?.status === "approved" ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          onClick={() => handleRevoke(a.skill_slug)}
                        >
                          Revoke
                        </Button>
                      ) : (
                        <Button
                          type="button"
                          size="sm"
                          disabled={busy}
                          onClick={() => handleGrant(a.skill_slug)}
                        >
                          Approve
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
