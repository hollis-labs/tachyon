import { useEffect, useState } from 'react'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'
import { Avatar, SessionDot } from './session-rows'
import { NudgeControl, RestartControl } from './controls'
import { ContextTab, HistoryTab, NowTab, TimelineTab } from './modal-tabs'
import { stateText } from './session-parts'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

type Tab = 'now' | 'timeline' | 'context' | 'history'
const TABS: readonly Tab[] = ['now', 'timeline', 'context', 'history']

/**
 * The session detail: header with the controls, four tabs. Opens on Now. [ and ]
 * step through tabs; Esc closes a popover first, then the modal; focus goes back
 * to the row that opened it (the dialog restores it).
 */
export function SessionModal({ session, paused, now, onClose, onRestarted }: { session: Session; paused: boolean; now: number; onClose: () => void; onRestarted: (newId: string | null) => void }) {
  const [tab, setTab] = useState<Tab>('now')
  const [nudgeSeq, setNudgeSeq] = useState(0)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || e.metaKey || e.ctrlKey || e.altKey) return
      const i = TABS.indexOf(tab)
      if (e.key === ']') setTab(TABS[Math.min(TABS.length - 1, i + 1)])
      else if (e.key === '[') setTab(TABS[Math.max(0, i - 1)])
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [tab])
  const M = AGENTS.modal
  const action = session.one_liner ? ` · ${session.one_liner.verb} ${session.one_liner.target}` : ''
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent showCloseButton={false} widthClassName="max-w-[980px]" className="flex h-[min(660px,calc(100dvh-5rem))] flex-col gap-0 overflow-hidden p-0 max-sm:h-dvh max-sm:max-w-none max-sm:rounded-none">
        <header className="flex flex-wrap items-center gap-x-2.5 gap-y-2 border-b border-border-subtle px-4 py-3">
          <Avatar name={session.agent.name} />
          <div className="flex min-w-0 flex-col">
            <DialogTitle className="font-sans text-[15px] font-semibold normal-case leading-tight tracking-normal text-fg">{session.agent.name}</DialogTitle>
            <DialogDescription className="text-label text-fg-muted">
              {session.kind} · {session.provider}{session.model ? ` · ${session.model}` : ''} · {M.sessionWord} {session.id.slice(0, 8)}
            </DialogDescription>
          </div>
          <span className="flex min-w-0 items-center gap-2 text-xs text-fg">
            <SessionDot s={session} />
            <span className="truncate">{stateText(session, now)}{action}</span>
          </span>
          <span className="flex-1" />
          <NudgeControl session={session} paused={paused} onSent={() => setNudgeSeq((n) => n + 1)} />
          <RestartControl session={session} paused={paused} onDone={onRestarted} />
          <DialogClose aria-label={M.close} className="grid size-7 place-items-center rounded-control border border-border text-fg-secondary hover:bg-surface-hover">✕</DialogClose>
        </header>
        <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="min-h-0 flex-1 gap-0">
          <TabsList aria-label={M.tabsLabel} className="h-auto w-full justify-start gap-0.5 rounded-none border-b border-border-subtle bg-transparent px-3 py-1.5">
            {TABS.map((t) => (
              <TabsTrigger key={t} value={t} className="h-[26px] flex-none rounded-full border border-transparent px-3 text-xs font-normal text-fg-muted data-[active]:border-border data-[active]:bg-selection data-[active]:font-semibold data-[active]:text-fg">{M.tabs[t]}</TabsTrigger>
            ))}
          </TabsList>
          <div className="min-h-0 flex-1 overflow-auto p-4">
            {tab === 'now' && <NowTab s={session} now={now} />}
            {tab === 'timeline' && <TimelineTab key={nudgeSeq} s={session} now={now} />}
            {tab === 'context' && <ContextTab s={session} />}
            {tab === 'history' && <HistoryTab s={session} />}
          </div>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
