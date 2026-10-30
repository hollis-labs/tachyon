import { useEffect, useState } from 'react'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@hollis-labs/design-components'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
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
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent widthClassName="max-w-4xl" className="max-h-[90vh] overflow-auto">
        <header className="flex flex-wrap items-center gap-3">
          <div className="min-w-0 flex-1">
            <DialogTitle>{session.agent.name}</DialogTitle>
            <DialogDescription>
              {session.kind} · {session.provider}{session.model ? ` · ${session.model}` : ''} · <span className="font-mono">{session.id}</span> · {stateText(session, now)}
            </DialogDescription>
          </div>
          <NudgeControl session={session} paused={paused} onSent={() => setNudgeSeq((n) => n + 1)} />
          <RestartControl session={session} paused={paused} onDone={onRestarted} />
        </header>
        <DashboardViewTabs label={M.tabsLabel} value={tab} onChange={(v) => setTab(v as Tab)} items={TABS.map((t) => ({ value: t, label: M.tabs[t] }))} />
        <div className="min-h-64">
          {tab === 'now' && <NowTab s={session} now={now} />}
          {tab === 'timeline' && <TimelineTab key={nudgeSeq} s={session} now={now} />}
          {tab === 'context' && <ContextTab s={session} />}
          {tab === 'history' && <HistoryTab s={session} />}
        </div>
      </DialogContent>
    </Dialog>
  )
}
