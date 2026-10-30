import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Button } from '@hollis-labs/design-components'
import { type Summary, builderApi } from './api'
import { useChangeMode } from './capabilities'
import { type BuilderTab, BUILDER_TITLE, DEFINITIONS_NOTE, TAB_LABEL, TAB_PATH } from './vocab'

const TABS: BuilderTab[] = ['agents', 'launches', 'library']

/** Page header shared by Agents, Launches and Library: title, area tabs with counts, note, New agent. */
export function BuilderShell({ tab, counts }: { tab: BuilderTab; counts?: Summary | null }) {
  const [own, setOwn] = useState<Summary | null>(null)
  const [mode] = useChangeMode()
  useEffect(() => {
    if (counts !== undefined) return
    builderApi.summary().then(setOwn, () => undefined)
  }, [counts])
  const c = counts ?? own
  const lib = c?.library
  const count: Record<string, number | undefined> = {
    agents: c?.agents?.total,
    launches: c?.launches?.total,
    library: lib ? lib.prompts + lib.skills + (lib.tool_servers ?? 0) + lib.policies : undefined,
  }
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 overflow-x-auto border-b border-border-subtle px-4">
      <h1 className="font-mono text-caption font-semibold uppercase tracking-label text-fg">{BUILDER_TITLE}</h1>
      <nav aria-label="Builder areas" className="flex shrink-0 gap-0.5">
        {TABS.map((t) => (
          <Link
            key={t}
            to={TAB_PATH[t]}
            aria-current={t === tab ? 'page' : undefined}
            className={`inline-flex h-7 items-center gap-1.5 rounded-control px-3 text-xs ${t === tab ? 'bg-selection font-semibold text-fg' : 'text-fg-muted hover:text-fg'}`}
          >
            {TAB_LABEL[t]}
            {count[t] !== undefined && <small className="font-mono text-caption text-fg-faint">{count[t]}</small>}
          </Link>
        ))}
      </nav>
      <span className="flex-1" />
      <p className="hidden shrink-0 text-label text-fg-muted sm:block">
        {DEFINITIONS_NOTE}{' '}
        <Link className="text-fg-secondary hover:text-fg" to="/agents">
          Agents ▸
        </Link>
      </p>
      {mode !== 'unavailable' && (
        <Button type="button" size="sm" disabled title="Creating an agent arrives with the edit flow" className="hidden sm:inline-flex">
          New agent
        </Button>
      )}
    </header>
  )
}
