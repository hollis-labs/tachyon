import { useMemo, useState, type KeyboardEvent, type ReactNode } from 'react'
import { SearchPalette } from '@hollis-labs/design-components'

export interface PaletteCommand {
  id: string
  label: ReactNode
  /** Text matched against the query. */
  text: string
  mode: string
  /** Leading group label shown in the row; groups follow the order of `commands`. */
  group?: string
}

export interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  /** Modes are the palette's filters, e.g. Create and Open. */
  modes: readonly { id: string; label: string }[]
  commands: readonly PaletteCommand[]
  /** Enter. */
  onRun: (id: string, query: string) => void
  /** Tab: run and link the result to the focused item. Omit to disable. */
  onRunAndLink?: (id: string, query: string) => void
  emptyLabel: string
  /** Footer key hints, rendered under the list. */
  hints?: ReactNode
}

/** Composition over SearchPalette: modes as filters, Tab-to-link, local matching. Opening is the host's (the "/" key). */
export function CommandPalette({ open, onOpenChange, title, modes, commands, onRun, onRunAndLink, emptyLabel, hints }: CommandPaletteProps) {
  const [query, setQuery] = useState('')
  const [mode, setMode] = useState(modes[0]?.id ?? '')
  const options = useMemo(() => {
    const q = query.trim().toLowerCase()
    return commands
      .filter((c) => c.mode === mode && (!q || c.text.toLowerCase().includes(q)))
      .map((c) => ({ id: c.id, label: c.group ? <span className="flex gap-2"><span className="text-fg-faint">{c.group}</span>{c.label}</span> : c.label }))
  }, [commands, mode, query])

  const close = (v: boolean) => {
    if (!v) setQuery('')
    onOpenChange(v)
  }
  // Tab bubbles up from the portalled dialog through React, so one handler here sees it.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Tab' || !onRunAndLink || options.length === 0) return
    // The input names the highlighted option by index in aria-activedescendant.
    const at = Number((e.target as HTMLElement).getAttribute('aria-activedescendant')?.split('-option-').pop())
    const id = (Number.isInteger(at) && options[at] ? options[at] : options[0]).id
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
          onRun(id, query)
          close(false)
        }}
        filters={modes}
        filterId={mode}
        onFilterChange={setMode}
        sourceGeneration={`${mode}:${commands.length}`}
        emptyLabel={emptyLabel}
      />
      {open && hints && <div className="sr-only">{hints}</div>}
    </div>
  )
}
