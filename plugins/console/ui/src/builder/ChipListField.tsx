import { type KeyboardEvent, useState } from 'react'
import { Input } from '@hollis-labs/design-components'

const CHIP = 'inline-flex h-5 items-center gap-1.5 whitespace-nowrap rounded border border-border px-1.5 font-mono text-caption text-fg-secondary'

/**
 * Chips with a remove cross and a dashed "+ Add". Read-only it is plain chips.
 * KIT-GAP: the design kit has no chip-list field; this is the Builder-owned one until the kit brief lands.
 */
export function ChipListField({ label, value, onChange, editable = false, empty = 'none' }: {
  label: string
  value: readonly string[]
  onChange?: (next: string[]) => void
  editable?: boolean
  empty?: string
}) {
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const commit = () => {
    const t = text.trim()
    if (t && !value.includes(t)) onChange?.([...value, t])
    setText('')
    setAdding(false)
  }
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.stopPropagation()
      setText('')
      setAdding(false)
    }
  }
  if (!editable && value.length === 0) return <span className="text-fg-faint">{empty}</span>
  return (
    <ul aria-label={label} className="flex flex-wrap items-center gap-1.5">
      {value.map((v) => (
        <li key={v} className={CHIP}>
          {v}
          {editable && (
            <button type="button" aria-label={`Remove ${v} from ${label}`} onClick={() => onChange?.(value.filter((x) => x !== v))} className="text-fg-faint hover:text-fg">
              ×
            </button>
          )}
        </li>
      ))}
      {editable && (
        <li>
          {adding ? (
            <Input autoFocus aria-label={`Add to ${label}`} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={onKey} onBlur={commit} className="h-5 w-36 font-mono text-caption" />
          ) : (
            <button type="button" onClick={() => setAdding(true)} className="inline-flex h-5 items-center rounded-full border border-dashed border-border px-2 text-label text-fg-muted hover:text-fg">
              + Add
            </button>
          )}
        </li>
      )}
    </ul>
  )
}
