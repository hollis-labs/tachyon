import { useEffect, useState } from 'react'
import { RefIcon } from '../autolink/RefIcon'
import type { RefDetailProps } from '../autolink/extensions'
import { fileApi, type FileStat } from './api'
import { chipIconToken, metaLine } from './FileViewerPane'

const basename = (p: string) => p.split('/').pop() ?? p
const dirname = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) || '/' : '')

/** Hovercard for a file chip: icon, name, "HTML · 59 KB · modified 12m ago", folder, open hint. */
export function FileHover({ refId }: RefDetailProps) {
  const path = refId.canonical
  const [stat, setStat] = useState<FileStat | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let live = true
    fileApi.stat(path).then((s) => live && setStat(s), () => live && setFailed(true))
    return () => {
      live = false
    }
  }, [path])
  const blocked = !!stat && (!stat.exists || !stat.allowed)
  const meta = failed ? 'Operator access is needed to view files.' : stat ? metaLine(stat, path) : 'Loading…'
  return (
    <div className="flex items-start gap-3" data-testid="file-hover">
      <span className="grid size-9 shrink-0 place-items-center rounded-control border border-border bg-selection text-fg-secondary">
        <RefIcon token={chipIconToken(path)} className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-control font-bold text-fg">{basename(path)}</div>
        <div className={`text-caption ${blocked ? 'text-danger' : 'text-fg-muted'}`}>{meta}</div>
        <div className="mt-1 truncate font-mono text-caption text-fg-muted" title={dirname(path)}>{dirname(path)}</div>
        {stat && !blocked ? <div className="mt-2 text-caption text-fg-muted">Click to preview</div> : null}
      </div>
    </div>
  )
}
