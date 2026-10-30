import { useMemo } from 'react'
import { CommandPalette, PaletteHint, type PaletteCommand } from '@planner/shared'
import type { ChatThread } from '../types'

export interface PaletteActions {
  threads: ChatThread[]
  active: ChatThread | null
  onOpenThread: (id: string) => void
  startOptions: { id: string; label: string; onSelect: () => void }[]
  /** Commands of the open chat. A command whose handler is absent is not listed. */
  onTogglePin?: () => void
  onCopyLink?: () => void
  onPanel?: (panel: 'plan' | 'work') => void
  onDrawer?: (mode: 'scratch' | 'terminal' | 'workspace') => void
}

const MODES = [
  { id: 'chat', label: 'This chat' },
  { id: 'create', label: 'Create' },
  { id: 'open', label: 'Open' },
]

/** The '/' palette over @planner/shared's CommandPalette: This chat, Create, Open. */
export function ChatPalette({ open, onOpenChange, a }: { open: boolean; onOpenChange: (o: boolean) => void; a: PaletteActions }) {
  const cmds = useMemo(() => {
    const run = new Map<string, () => void>()
    const list: PaletteCommand[] = []
    const add = (c: PaletteCommand, f: () => void) => { list.push(c); run.set(c.id, f) }
    if (a.active) {
      if (a.onPanel) {
        add({ id: 'panel-plan', mode: 'chat', group: 'Panel', label: 'Open Plan', text: 'open plan panel', hint: '⌘.' }, () => a.onPanel?.('plan'))
        add({ id: 'panel-work', mode: 'chat', group: 'Panel', label: 'Open Work (mini Planner)', text: 'open work planner panel' }, () => a.onPanel?.('work'))
      }
      if (a.onDrawer) {
        add({ id: 'drawer-scratch', mode: 'chat', group: 'Drawer', label: 'Open Scratchpad', text: 'open scratchpad drawer', hint: '⌘J' }, () => a.onDrawer?.('scratch'))
        add({ id: 'drawer-term', mode: 'chat', group: 'Drawer', label: 'Open Terminal', text: 'open terminal drawer', hint: '⌃T' }, () => a.onDrawer?.('terminal'))
        add({ id: 'drawer-ws', mode: 'chat', group: 'Drawer', label: 'Open Workspace', text: 'open workspace drawer', hint: '⌘⇧W' }, () => a.onDrawer?.('workspace'))
      }
      if (a.onTogglePin) add({ id: 'pin', mode: 'chat', group: 'Chat', label: a.active.pinned ? 'Unpin this chat' : 'Pin this chat', text: 'pin unpin chat' }, a.onTogglePin)
      if (a.onCopyLink) add({ id: 'link', mode: 'chat', group: 'Chat', label: 'Copy link to this chat', text: 'copy link chat' }, a.onCopyLink)
    }
    for (const o of a.startOptions) add({ id: `start-${o.id}`, mode: 'create', group: 'Chat', label: `Start a chat with ${o.label}`, text: `start new chat ${o.label}` }, o.onSelect)
    for (const t of a.threads) add({ id: `open-${t.id}`, mode: 'open', group: 'Chat', label: `${t.title}${t.lastPreview ? ` · ${t.lastPreview.slice(0, 50)}` : ''}`, text: `${t.title} ${t.lastPreview ?? ''}` }, () => a.onOpenThread(t.id))
    return { list, run }
  }, [a])
  return (
    <CommandPalette
      open={open}
      onOpenChange={onOpenChange}
      title="Chat commands"
      modes={MODES}
      commands={cmds.list}
      onRun={(id) => cmds.run.get(id)?.()}
      emptyLabel="Nothing matches."
      hints={<><PaletteHint k="↵">run</PaletteHint><PaletteHint k="Esc">close</PaletteHint></>}
    />
  )
}
