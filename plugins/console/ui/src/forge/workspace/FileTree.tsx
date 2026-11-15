import { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { SearchInput } from '@hollis-labs/design-components'
import { buildFileTree, statusGlyph, type TreeNode } from './diff-utils'
import type { FileDiff } from '../types'

const GLYPH: Record<string, string> = { added: 'text-success', removed: 'text-danger', renamed: 'text-fg-muted', modified: 'text-fg-muted' }

export interface FileTreeProps {
  files: FileDiff[]
  active?: string
  /** Comment-thread counts by path. */
  threads: Map<string, number>
  onSelect: (path: string) => void
}

export function FileTree({ files, active, threads, onSelect }: FileTreeProps) {
  const [filter, setFilter] = useState('')
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return q ? files.filter((f) => f.path.toLowerCase().includes(q)) : files
  }, [files, filter])
  const tree = useMemo(() => buildFileTree(shown), [shown])

  const toggle = (path: string) =>
    setClosed((c) => {
      const n = new Set(c)
      if (!n.delete(path)) n.add(path)
      return n
    })

  const render = (nodes: TreeNode[], depth: number) =>
    nodes.map((n) =>
      n.dir ? (
        <li key={n.path}>
          <button
            type="button"
            onClick={() => toggle(n.path)}
            aria-expanded={!closed.has(n.path)}
            style={{ paddingLeft: 8 + depth * 12 }}
            className="flex w-full items-center gap-1 rounded-sm py-1 pr-2 text-left text-xs text-fg-muted hover:bg-surface focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
          >
            {closed.has(n.path) ? <ChevronRight className="h-3 w-3 shrink-0" /> : <ChevronDown className="h-3 w-3 shrink-0" />}
            <span className="min-w-0 truncate">{n.name}</span>
          </button>
          {!closed.has(n.path) && <ul>{render(n.children, depth + 1)}</ul>}
        </li>
      ) : (
        <li key={n.path}>
          <button
            type="button"
            onClick={() => onSelect(n.path)}
            aria-current={active === n.path || undefined}
            title={n.path}
            style={{ paddingLeft: 8 + depth * 12 }}
            className={`flex w-full items-center gap-2 rounded-sm py-1 pr-2 text-left text-xs hover:bg-surface focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring ${active === n.path ? 'bg-surface text-fg' : 'text-fg-secondary'}`}
          >
            <span className={`w-3 shrink-0 text-center font-mono ${GLYPH[n.file!.status] ?? 'text-fg-muted'}`}>{statusGlyph(n.file!)}</span>
            <span className="min-w-0 flex-1 truncate">{n.name}</span>
            {(threads.get(n.path) ?? 0) > 0 && <span className="shrink-0 text-fg-muted" aria-label={`${threads.get(n.path)} comment threads`}>{threads.get(n.path)}</span>}
          </button>
        </li>
      ),
    )

  return (
    <nav aria-label="Changed files" className="flex min-h-0 flex-col" data-testid="forge-file-tree">
      <div className="dash-search-adapter p-2 pb-0">
        <SearchInput value={filter} onChange={setFilter} placeholder={`Filter ${files.length} files…`} ariaLabel="Filter files" debounceMs={0} />
      </div>
      <ul className="min-h-0 flex-1 overflow-auto p-2">{tree.length ? render(tree, 0) : <li className="px-3 py-2 text-xs text-fg-faint">No matching files.</li>}</ul>
    </nav>
  )
}
