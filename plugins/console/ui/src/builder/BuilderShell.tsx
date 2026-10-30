import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Segmented } from '../components/segmented'
import { type Summary, builderApi } from './api'
import { type BuilderTab, BUILDER_TITLE, DEFINITIONS_NOTE, TAB_LABEL, TAB_PATH } from './vocab'

/** Title, note and area tabs shared by Agents, Launches and Library. */
export function BuilderShell({ tab, counts }: { tab: BuilderTab; counts?: Summary | null }) {
  const navigate = useNavigate()
  const [own, setOwn] = useState<Summary | null>(null)
  useEffect(() => {
    if (counts !== undefined) return
    builderApi.summary().then(setOwn, () => undefined)
  }, [counts])
  const c = counts ?? own
  return (
    <>
      <header className="flex flex-wrap items-baseline gap-x-4 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold text-fg">{BUILDER_TITLE}</h1>
        <p className="dash-support text-fg-muted">
          {DEFINITIONS_NOTE}{' '}
          <Link className="underline" to="/agents">
            Agents
          </Link>
          .
        </p>
      </header>
      <Segmented<BuilderTab>
        label="Builder areas"
        value={tab}
        onChange={(t) => navigate(TAB_PATH[t])}
        items={[
          { value: 'agents', label: TAB_LABEL.agents, count: c?.agents?.total },
          { value: 'launches', label: TAB_LABEL.launches, count: c?.launches?.total },
          { value: 'library', label: TAB_LABEL.library },
        ]}
      />
    </>
  )
}
