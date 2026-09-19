import { Combobox, notifyError, Pill, Skeleton } from "@hollis-labs/sysop-ui"
import { X } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { MCPServer } from "../../api/client"
import { useApi } from "../../api/context"

interface AgentMCPPanelProps {
  agentId: string
  mcpServersJson?: string
  onChanged: () => void
}

function parseServerNames(json?: string): string[] {
  if (!json) return []
  try {
    const parsed = JSON.parse(json)
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : []
  } catch {
    return []
  }
}

export function AgentMCPPanel({ agentId, mcpServersJson, onChanged }: AgentMCPPanelProps) {
  const api = useApi()
  const [catalog, setCatalog] = useState<MCPServer[]>([])
  const [loading, setLoading] = useState(true)
  const [picked, setPicked] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setLoading(true)
      setCatalog(await api.listMCPServerCatalog())
    } catch (error) {
      notifyError(error, "Failed to load MCP servers")
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    load()
  }, [load])

  const attached = useMemo(() => parseServerNames(mcpServersJson), [mcpServersJson])
  const pickable = useMemo(
    () =>
      catalog
        .filter((s) => !attached.includes(s.name))
        .map((s) => ({ value: s.name, label: s.name })),
    [catalog, attached],
  )

  async function handleAttach(name: string | null) {
    if (!name) return
    setBusy(true)
    try {
      await api.attachAgentMCPServer(agentId, name)
      onChanged()
    } catch (error) {
      notifyError(error, "Failed to attach MCP server")
    } finally {
      setPicked(null)
      setBusy(false)
    }
  }

  async function handleDetach(name: string) {
    setBusy(true)
    try {
      await api.detachAgentMCPServer(agentId, name)
      onChanged()
    } catch (error) {
      notifyError(error, "Failed to detach MCP server")
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return <Skeleton className="h-10 w-full rounded-md" />
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {attached.length === 0 && (
          <p className="text-sm text-text-subtle">No MCP servers attached.</p>
        )}
        {attached.map((name) => (
          <Pill key={name} tone="info">
            <span className="flex items-center gap-1.5">
              {name}
              <button
                type="button"
                disabled={busy}
                onClick={() => handleDetach(name)}
                className="rounded-full hover:bg-black/10"
                title={`Detach ${name}`}
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          </Pill>
        ))}
      </div>
      <div className="max-w-xs">
        <Combobox
          items={pickable}
          value={picked}
          onChange={(value) => {
            setPicked(value)
            handleAttach(value)
          }}
          ariaLabel="Attach an MCP server"
          placeholder="Attach an MCP server…"
          emptyText="No more registered servers to attach"
        />
      </div>
    </div>
  )
}
