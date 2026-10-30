import { useState, type KeyboardEvent } from 'react'
import { ArrowRight, ChevronRight, ListChecks, Lock, Scale, SquareTerminal } from 'lucide-react'
import { Chip, Kbd, SegmentedProgress, StateDot } from '@planner/shared'
import { Act, Block, LINK } from './Block'
import { useAnswer } from './useAnswer'
import type { StreamBlock } from './types'

type Of<K extends StreamBlock['kind']> = Extract<StreamBlock, { kind: K }>

/** Result line shown when a card collapses: one line, announced once, with Undo while the window is open. */
function Result({ children, undoLeft, onUndo, note }: { children: React.ReactNode; undoLeft?: number; onUndo?: () => void; note?: string }) {
  return (
    <div role="status" className="flex flex-wrap items-center gap-2 text-control">
      <StateDot state="done" />
      <span>{children}</span>
      {note && <span className="text-xs text-fg-muted">{note}</span>}
      <span className="flex-1" />
      {onUndo && undoLeft ? <Act onClick={onUndo} kbd="⌘Z">Undo {undoLeft}s</Act> : null}
    </div>
  )
}

const Err = ({ children }: { children: string }) => (children ? <p role="alert" className="text-xs text-danger">{children} The card is still open.</p> : null)

export type Commit = (block: StreamBlock, value: unknown) => Promise<void>

export function DecisionCard({ block, commit }: { block: Of<'decision'>; commit?: Commit }) {
  const [note, setNote] = useState('')
  const a = useAnswer<string>({ reversible: block.reversible, initial: block.answered?.optionId, commit: commit && ((v) => commit(block, { optionId: v, note })) })
  const chosen = block.options.find((o) => o.id === (a.value ?? block.answered?.optionId))
  const idx = chosen ? block.options.indexOf(chosen) : -1
  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    if ((e.target as HTMLElement).closest('input, textarea')) return
    const n = Number(e.key)
    if (n >= 1 && n <= block.options.length) a.answer(block.options[n - 1].id)
    if ((e.metaKey || e.ctrlKey) && e.key === 'z') a.undo()
  }
  const answered = a.phase === 'answered' || a.phase === 'saving'
  return (
    <Block label={`Decision: ${block.title}`} icon={<Scale className="size-3.5" />} caption="Decision" title={block.title} aside={block.cite && <Chip square>{block.cite}</Chip>} onKeyDown={answered ? undefined : onKey}>
      {answered && chosen ? (
        <Result undoLeft={a.undoLeft} onUndo={block.reversible ? a.undo : undefined} note={a.phase === 'saving' ? 'Saving' : block.answered?.next ? `recorded · next: ${block.answered.next}` : 'recorded'}>
          You chose <b className="font-semibold">{String.fromCharCode(65 + idx)} · {chosen.label}</b>
        </Result>
      ) : (
        <>
          <div role="radiogroup" aria-label={`Options for ${block.title}`} className="flex flex-col">
            {block.options.map((o, i) => (
              <div key={o.id} role="radio" aria-checked={false} className="flex items-center gap-3 border-t border-border-subtle py-2 first:border-t-0 max-sm:flex-wrap">
                <span aria-hidden className="w-3.5 font-mono text-label font-semibold text-fg-muted">{String.fromCharCode(65 + i)}</span>
                <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-control max-sm:min-w-[60%]">
                  <b className="font-semibold">{o.label}</b>
                  {o.recommended && <span className="self-center font-mono text-caption uppercase tracking-label text-fg-secondary">recommended</span>}
                  {o.description && <span className="w-full text-xs text-fg-muted">{o.description}</span>}
                </div>
                <Act primary={o.recommended} kbd={String(i + 1)} aria-label={`Choose ${o.label}`} onClick={() => a.answer(o.id)}>Choose</Act>
              </div>
            ))}
          </div>
          <input aria-label="Add a note (optional)" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Add a note (optional)…" className="h-[30px] rounded-control border border-dashed border-border bg-bg px-2.5 text-xs placeholder:text-fg-secondary focus-visible:bg-bg!" />
          <Err>{a.error}</Err>
        </>
      )}
    </Block>
  )
}

export function PlanCard({ block, onOpenPanel }: { block: Of<'plan'>; onOpenPanel?: () => void }) {
  const { done, doing } = block
  const label = `${done} of ${block.total} done`
  return (
    <Block label={`Plan: ${label}`} icon={<ListChecks className="size-3.5" />} caption="Plan" title={label} aside={<button type="button" onClick={onOpenPanel} className={LINK}>Open in panel <Kbd>⌘.</Kbd></button>}>
      <SegmentedProgress label={label} items={[{ state: 'done', count: done }, { state: 'doing', count: doing }, { state: 'todo', count: Math.max(0, block.total - done - doing) }]} />
      <ul className="flex flex-col gap-0.5">
        {block.steps.map((s) => (
          <li key={s.id} className={`flex items-center gap-2 py-[3px] text-xs ${s.state === 'done' ? 'text-fg-muted line-through' : 'text-fg-secondary'}`}>
            <i aria-hidden className={`size-3 shrink-0 rounded-sm border ${s.state === 'done' ? 'border-fg/60 bg-fg/60' : s.state === 'doing' ? 'border-fg bg-fg/20' : 'border-fg-muted'}`} />
            <span className="sr-only">{s.state}: </span>{s.title}
          </li>
        ))}
      </ul>
    </Block>
  )
}

export function ApprovalCard({ block, commit }: { block: Of<'approval'>; commit?: Commit }) {
  const [details, setDetails] = useState(false)
  const a = useAnswer<'approved' | 'always' | 'denied'>({ reversible: block.reversible, initial: block.answered?.decision, commit: commit && ((v) => commit(block, v)) })
  const onKey = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key.toLowerCase() === 'y') a.answer('approved')
    if (e.key.toLowerCase() === 'n') a.answer('denied')
    if ((e.metaKey || e.ctrlKey) && e.key === 'z') a.undo()
  }
  const answered = a.phase === 'answered' || a.phase === 'saving'
  const decision = a.value ?? block.answered?.decision
  return (
    <Block label={`Approval: ${block.title}`} icon={<Lock className="size-3.5" />} caption="Approval" title={block.title} aside={<Chip square>{block.tool}{block.count && block.count > 1 ? ` ×${block.count}` : ''}</Chip>} onKeyDown={answered ? undefined : onKey}>
      {answered ? (
        <Result undoLeft={a.undoLeft} onUndo={block.reversible ? a.undo : undefined} note={a.phase === 'saving' ? 'Saving' : decision !== 'denied' && !block.reversible ? 'Already ran, cannot be undone' : undefined}>
          {decision === 'denied' ? <>Denied · <span className="text-danger">not run</span></> : decision === 'always' ? 'Allowed, and always in this chat' : 'Allowed once'}
        </Result>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <Act primary kbd="Y" onClick={() => a.answer('approved')}>Allow once</Act>
            {block.canAlways && <Act onClick={() => a.answer('always')}>Always in this chat</Act>}
            <Act danger kbd="N" onClick={() => a.answer('denied')}>Deny</Act>
            <span className="flex-1" />
            <button type="button" aria-expanded={details} onClick={() => setDetails((d) => !d)} className={LINK}>Details <ChevronRight aria-hidden className={`inline size-3 ${details ? 'rotate-90' : ''}`} /></button>
          </div>
          {details && <p className="font-mono text-xs text-fg-secondary">{block.tool}{block.count && block.count > 1 ? ` × ${block.count}` : ''} · {block.title}</p>}
          <Err>{a.error}</Err>
        </>
      )}
    </Block>
  )
}

const FIELD = 'flex h-[30px] items-center rounded-control border border-border bg-bg px-2.5 text-xs text-fg-secondary'

export function DispatchCard({ block, commit }: { block: Of<'dispatch'>; commit?: Commit }) {
  const [title, setTitle] = useState(block.fields.title)
  const a = useAnswer<boolean>({ reversible: false, initial: block.answered ? true : undefined, commit: commit && (() => commit(block, { title })) })
  const create = () => a.answer(true)
  return (
    <Block label={`Dispatch to ${block.target}`} icon={<ArrowRight className="size-3.5" />} caption="Dispatch" title={`to ${block.target} · ${block.effect}`} onKeyDown={a.phase === 'pending' ? (e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') create() } : undefined}>
      {a.phase === 'pending' ? (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <input aria-label="Task title" value={title} onChange={(e) => setTitle(e.target.value)} className={`${FIELD} min-w-[50%] flex-1 focus-visible:bg-bg!`} />
            <span className={FIELD}>{block.fields.project} ▾</span>
            <span className={FIELD}>{block.fields.priority} ▾</span>
            <Act primary kbd="⌘↵" onClick={create}>Create</Act>
          </div>
          <Err>{a.error}</Err>
        </>
      ) : (
        <Result note={a.phase === 'saving' ? 'Saving' : undefined}>Created <b className="font-semibold">{title}</b>{block.answered?.taskId && <> · <Chip square>{block.answered.taskId}</Chip></>}</Result>
      )}
    </Block>
  )
}

export function ToolCallsRow({ block }: { block: Of<'tools'> }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="mx-auto w-full max-w-[760px]">
      <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex h-[30px] w-full items-center gap-2 rounded-control border border-border px-2.5 text-left font-mono text-xs text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
        <ChevronRight aria-hidden className={`size-3 ${open ? 'rotate-90' : ''}`} />
        <span>{block.tools.length} tool call{block.tools.length === 1 ? '' : 's'}</span>
        <span className="min-w-0 flex-1 truncate text-fg-muted">{block.tools.join(' · ')}</span>
        <span className={block.state === 'failed' ? 'text-danger' : 'text-fg-muted'}>{block.state}</span>
      </button>
      {open && <ul aria-label="Tool calls" className="mt-1 flex flex-col gap-0.5 pl-6 font-mono text-xs text-fg-secondary">{block.tools.map((t, i) => <li key={`${t}-${i}`}>{t}</li>)}</ul>}
    </div>
  )
}
