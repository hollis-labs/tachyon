import { useMemo, useState, type KeyboardEvent, type ReactNode } from 'react'
import { SearchPalette, type SearchPaletteOption } from '@hollis-labs/design-components'
import { CAPTION, Kbd } from './primitives'

export interface PaletteCommand {
  id: string
  label: ReactNode
  /** Text matched against the query. */
  text: string
  /** The command's section: it must be one of the palette's `modes`. */
  mode: string
  /** Leading kind label of the row (Theme, Task, Idea…). */
  group?: string
  /** Trailing hint of the row: a key, or "→ link". */
  hint?: ReactNode
  /** Shown whatever the query is (a create row echoing what was typed). */
  always?: boolean
}

export interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  /** Sections, in order: each is a heading over its commands. */
  modes: readonly { id: string; label: string }[]
  commands: readonly PaletteCommand[] | ((query: string) => readonly PaletteCommand[])
  /** Enter. */
  onRun: (id: string, query: string) => void
  /** Tab: run and link the result to the focused item. Omit to disable. */
  onRunAndLink?: (id: string, query: string) => void
  emptyLabel: string
  /** Footer key hints, rendered under the list. */
  hints?: ReactNode
}

/** Composition over SearchPalette: one list in sections, Tab-to-link, local matching. Opening is the host's (the "/" key). */
export function CommandPalette({ open, onOpenChange, title, modes, commands, onRun, onRunAndLink, emptyLabel, hints }: CommandPaletteProps) {
  const [query, setQuery] = useState('')
  const all = typeof commands === 'function' ? commands(query.trim()) : commands
  const options = useMemo<SearchPaletteOption[]>(() => {
    const q = query.trim().toLowerCase()
    return modes.flatMap((m) => {
      const rows = all.filter((c) => c.mode === m.id && (c.always || !q || c.text.toLowerCase().includes(q)))
      if (rows.length === 0) return []
      return [
        { id: `heading:${m.id}`, disabled: true, label: <span className={CAPTION}>{m.label}</span> },
        ...rows.map((c) => ({
          id: c.id,
          label: (
            <span className="flex w-full items-center gap-3">
              {c.group && <span className="w-14 shrink-0 font-mono text-caption text-fg-faint">{c.group}</span>}
              <span className="min-w-0 flex-1 truncate">{c.label}</span>
              {c.hint && <span className="shrink-0 font-mono text-caption text-fg-muted">{c.hint}</span>}
            </span>
          ),
        })),
      ]
    })
  }, [all, modes, query])
  const runnable = options.filter((o) => !o.disabled)

  const close = (v: boolean) => {
    if (!v) setQuery('')
    onOpenChange(v)
  }
  // Tab bubbles up from the portalled dialog through React, so one handler here sees it.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Tab' || !onRunAndLink || runnable.length === 0) return
    // The input names the highlighted option by index in aria-activedescendant.
    const at = Number((e.target as HTMLElement).getAttribute('aria-activedescendant')?.split('-option-').pop())
    const id = (Number.isInteger(at) && options[at] && !options[at].disabled ? options[at] : runnable[0]).id
    e.preventDefault()
    onRunAndLink(id, query)
    close(false)
  }
  return (
    <div onKeyDown={onKeyDown}>
      <SearchPalette
        open={open}
        onOpenChange={close}
        title={title}
        query={query}
        onQueryChange={setQuery}
        options={options}
        onSelect={(id) => {
          onRun(id, query.trim())
          close(false)
        }}
        sourceGeneration={`${all.length}:${modes.length}`}
        emptyLabel={emptyLabel}
      />
      {open && hints && (
        <div className="pointer-events-none fixed inset-x-0 bottom-6 z-[60] mx-auto flex w-fit max-w-[calc(100vw-2rem)] flex-wrap items-center gap-x-4 gap-y-1 rounded-panel border border-border bg-bg-elevated px-3 py-1.5 text-label text-fg-muted max-sm:hidden">
          {hints}
        </div>
      )}
    </div>
  )
}

/** A key hint for the palette footer: `<Hint k="↵">create or open</Hint>`. */
export function PaletteHint({ k, children }: { k: string; children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <Kbd>{k}</Kbd>
      {children}
    </span>
  )
}
