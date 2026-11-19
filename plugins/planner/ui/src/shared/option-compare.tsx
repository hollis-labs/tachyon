import { useState } from 'react'
import { Button, cn } from '@hollis-labs/design-components'
import { CAPTION, Chip, Kbd } from './primitives'

export interface CompareOption {
  id: string
  label: string
  summary?: string
  pros?: string
  cons?: string
  recommended?: boolean
}

export interface OptionCompareLabels {
  choose: (label: string) => string
  chooseButton: string
  recommended: string
  chosen: string
  pros: string
  cons: string
  groupLabel: string
  /** "Comment": the card's second button; omit with `onComment`. */
  comment?: string
  /** "Add option": the foot row; omit with `onAdd`. */
  addOption?: string
  addLabel?: string
  addPros?: string
  addCons?: string
  addSave?: string
  /** Sentence after the add row: what a choice does. */
  footNote?: string
}

const letter = (i: number) => String.fromCharCode(65 + i)

/**
 * Options side by side with pros and cons, A / B / C. Press 1..n to focus an option, Enter on Choose commits.
 * Once chosen the others dim. The recommended option's button is the primary one.
 */
export function OptionCompare({ options, chosenId, onChoose, onComment, onAdd, busy, labels, className }: {
  options: readonly CompareOption[]
  chosenId?: string
  onChoose: (id: string) => void
  onComment?: (id: string) => void
  onAdd?: (draft: { label: string; pros: string; cons: string }) => void | Promise<void>
  busy?: boolean
  labels: OptionCompareLabels
  className?: string
}) {
  const [adding, setAdding] = useState(false)
  const [draft, setDraft] = useState({ label: '', pros: '', cons: '' })
  const field = (key: 'label' | 'pros' | 'cons', label?: string) => (
    <input aria-label={label} placeholder={label} value={draft[key]} onChange={(e) => setDraft({ ...draft, [key]: e.target.value })} className="h-8 rounded-control border border-border bg-transparent px-2 text-control text-fg outline-none focus-visible:border-fg-muted" />
  )
  return (
    <div className="flex flex-col gap-3">
      <div
        role="group"
        aria-label={labels.groupLabel}
        className={cn('grid gap-3 md:grid-cols-2', className)}
        onKeyDown={(e) => {
          const n = Number(e.key)
          if (e.metaKey || e.ctrlKey || e.altKey || !Number.isInteger(n) || n < 1 || n > options.length) return
          if ((e.target as HTMLElement).tagName === 'INPUT' || (e.target as HTMLElement).tagName === 'TEXTAREA') return
          e.preventDefault()
          ;(e.currentTarget.querySelectorAll<HTMLElement>('[data-option]')[n - 1])?.focus()
        }}
      >
        {options.map((o, i) => {
          const dim = chosenId !== undefined && chosenId !== o.id
          return (
            <article key={o.id} data-option tabIndex={0} aria-label={`${i + 1}. ${o.label}`} className={cn('flex flex-col gap-2.5 rounded-panel border p-4 outline-none focus-visible:outline-2', o.recommended ? 'border-fg-muted' : 'border-border', dim && 'opacity-50')}>
              <header className="flex items-center gap-2">
                <h3 className="font-sans text-control font-semibold normal-case tracking-normal text-fg">{letter(i)} · {o.label}</h3>
                {o.recommended && <Chip>{labels.recommended}</Chip>}
                {chosenId === o.id && <Chip tone="strong">{labels.chosen}</Chip>}
              </header>
              {o.summary && <p className="text-control text-fg-muted">{o.summary}</p>}
              {o.pros && <p className="flex gap-2 text-control text-fg"><span className={cn(CAPTION, 'pt-0.5')}>{labels.pros}</span>{o.pros}</p>}
              {o.cons && <p className="flex gap-2 text-control text-fg"><span className={cn(CAPTION, 'pt-0.5')}>{labels.cons}</span>{o.cons}</p>}
              {chosenId === undefined && (
                <div className="flex gap-2">
                  <Button size="sm" variant={o.recommended ? 'default' : 'outline'} disabled={busy} aria-label={labels.choose(o.label)} onClick={() => onChoose(o.id)}>
                    {labels.chooseButton} {letter(i)} <Kbd>{i + 1}</Kbd>
                  </Button>
                  {onComment && labels.comment && <Button size="sm" variant="outline" onClick={() => onComment(o.id)}>{labels.comment}</Button>}
                </div>
              )}
            </article>
          )
        })}
      </div>
      {onAdd && labels.addOption && chosenId === undefined && (
        adding ? (
          <form className="flex flex-wrap items-center gap-2" onSubmit={async (e) => { e.preventDefault(); if (!draft.label.trim()) return; await onAdd(draft); setDraft({ label: '', pros: '', cons: '' }); setAdding(false) }}>
            {field('label', labels.addLabel)}
            {field('pros', labels.addPros)}
            {field('cons', labels.addCons)}
            <Button size="sm" type="submit" disabled={busy || !draft.label.trim()}>{labels.addSave}</Button>
          </form>
        ) : (
          <p className="flex flex-wrap items-center gap-1.5 text-control text-fg-muted">
            <button type="button" onClick={() => setAdding(true)} className="inline-flex items-center gap-1.5 hover:text-fg">+ {labels.addOption} <Kbd>0</Kbd></button>
            {labels.footNote && <>· {labels.footNote}</>}
          </p>
        )
      )}
    </div>
  )
}
