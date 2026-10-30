import { useEffect, useRef, type ElementType, type KeyboardEvent } from 'react'
import { cn } from '@hollis-labs/design-components'

export interface InlineEditProps {
  value: string
  /** Called with the new text on blur or Cmd/Ctrl+Enter, only when it changed and is not empty. */
  onCommit: (value: string) => void
  /** Accessible name of the field. */
  label: string
  as?: ElementType
  multiline?: boolean
  disabled?: boolean
  className?: string
  id?: string
}

/**
 * Text you edit where it is shown: no box until you hover. Saves on blur or
 * Cmd/Ctrl+Enter, Escape puts the old text back. Plain text only.
 */
export function InlineEdit({ value, onCommit, label, as: Tag = 'span', multiline, disabled, className, id }: InlineEditProps) {
  const ref = useRef<HTMLElement>(null)
  // The DOM owns the text while it is being edited; a changed prop replaces it only when not focused.
  useEffect(() => {
    const el = ref.current
    if (el && document.activeElement !== el && el.textContent !== value) el.textContent = value
  }, [value])
  const commit = () => {
    const next = (ref.current?.textContent ?? '').trim()
    if (next && next !== value) onCommit(next)
    else if (ref.current) ref.current.textContent = value
  }
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      if (ref.current) ref.current.textContent = value
      ref.current?.blur()
    } else if (e.key === 'Enter' && (!multiline || e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      ref.current?.blur()
    }
  }
  return (
    <Tag
      ref={ref}
      id={id}
      role="textbox"
      aria-label={label}
      aria-multiline={multiline || undefined}
      aria-disabled={disabled || undefined}
      contentEditable={!disabled}
      suppressContentEditableWarning
      spellCheck={false}
      onBlur={commit}
      onKeyDown={onKeyDown}
      onPaste={(e: React.ClipboardEvent) => {
        e.preventDefault()
        document.execCommand('insertText', false, e.clipboardData.getData('text/plain'))
      }}
      className={cn('-mx-1.5 rounded-control px-1.5 py-0.5 outline-none hover:bg-selection focus-visible:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', multiline && 'whitespace-pre-wrap', className)}
    >
      {value}
    </Tag>
  )
}
