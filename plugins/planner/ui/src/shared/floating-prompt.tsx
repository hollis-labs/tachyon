import { useEffect, useId, useRef, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { Button } from '@hollis-labs/design-components'
import { CAPTION, Kbd } from './primitives'

export interface FloatingPromptProps {
  /** Small overline, e.g. "Moved to Done". */
  heading: string
  /** What the prompt is about: a cite and a title. */
  subject?: ReactNode
  /** Label of the one input, e.g. "What shipped?". */
  label: string
  /** Shown after the label when the line may be skipped. */
  optionalLabel?: string
  value: string
  onChange: (value: string) => void
  /** Reassurance or rule under the input. */
  note: string
  submitLabel: string
  skipLabel: string
  /** Enter, when the line has text (or `allowEmptySubmit`). */
  onSubmit: () => void
  /** Esc or Skip. */
  onSkip: () => void
  allowEmptySubmit?: boolean
}

/**
 * A small card that floats over the page without blocking it: one line of text,
 * Skip (Esc) and Add (Enter). The write it follows has already been saved.
 */
export function FloatingPrompt({ heading, subject, label, optionalLabel, value, onChange, note, submitLabel, skipLabel, onSubmit, onSkip, allowEmptySubmit }: FloatingPromptProps) {
  const input = useRef<HTMLInputElement>(null)
  const id = useId()
  useEffect(() => input.current?.focus(), [])
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (allowEmptySubmit || value.trim()) onSubmit()
  }
  const key = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onSkip()
    }
  }
  return (
    <form role="group" aria-labelledby={`${id}-h`} onSubmit={submit} onKeyDown={key} className="fixed left-1/2 top-1/3 z-50 flex w-110 max-w-[calc(100vw-2rem)] -translate-x-1/2 flex-col gap-2.5 rounded-panel border border-surface-hover bg-bg-elevated p-3.5 shadow-lg">
      <span id={`${id}-h`} className={CAPTION}>{heading}</span>
      {subject && <div className="text-control">{subject}</div>}
      <label className="flex h-9 items-center gap-2 rounded-control border border-fg-muted px-2.5 text-xs">
        <span className="shrink-0 text-fg-muted">{label}</span>
        {optionalLabel && <span className="order-last ml-auto shrink-0 font-mono text-caption text-fg-faint">{optionalLabel}</span>}
        <input ref={input} aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} className="min-w-0 flex-1 bg-transparent text-fg outline-none" />
      </label>
      <div className="flex items-center gap-2">
        <span className="text-label text-fg-muted">{note}</span>
        <span className="flex-1" />
        <Button type="button" size="sm" variant="outline" aria-label={skipLabel} onClick={onSkip}>{skipLabel} <Kbd>Esc</Kbd></Button>
        <Button type="submit" size="sm" aria-label={submitLabel} disabled={!allowEmptySubmit && !value.trim()}>{submitLabel} <Kbd className="border-primary-fg/30 text-primary-fg">↵</Kbd></Button>
      </div>
    </form>
  )
}
