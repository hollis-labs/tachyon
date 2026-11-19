import { useEffect, useRef } from 'react'
import { Button } from '@hollis-labs/design-components'
import type { ChangeMode } from './capabilities'
import { PreparedLabel } from './ModeNote'
import { changingText } from './changes'
import { overlayOpen } from './keys'

/**
 * The edit bar at the foot of an editor: what changed, Discard, Review changes, and the primary action.
 * The primary action says what will happen: handoff mode never says Save or Submit.
 * Keys while the bar is shown: Cmd/Ctrl+S opens Review (or the hand-off), Esc discards (asking first when dirty).
 * Builder-owned; Registry consumes it.
 */
export function DirtyBar({ labels, prepared, error, busy, mode, onDiscard, onReview, onViewPrepared }: {
  labels: (string | undefined)[]
  prepared: boolean
  error?: string
  busy: boolean
  mode: ChangeMode | undefined
  onDiscard: () => void
  onReview: () => void
  onViewPrepared: () => void
}) {
  const none = labels.length === 0
  const handoff = mode === 'handoff'
  const keys = useRef({ none, busy, prepared, onDiscard, onReview, onViewPrepared })
  keys.current = { none, busy, prepared, onDiscard, onReview, onViewPrepared }
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const k = keys.current
      if (e.defaultPrevented || overlayOpen()) return
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        if (k.prepared) k.onViewPrepared()
        else if (!k.none && !k.busy) k.onReview()
      } else if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.altKey && !k.prepared) {
        k.onDiscard()
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])
  return (
    <div role="status" className="flex shrink-0 flex-wrap items-center gap-2.5 border-t border-border bg-bg-elevated px-4 py-2.5 text-xs text-fg-secondary">
      {prepared ? (
        <PreparedLabel />
      ) : (
        <span className="mr-auto">
          {none ? (
            changingText(labels)
          ) : (
            <>
              <b className="font-semibold text-fg">{labels.length} unsaved change{labels.length === 1 ? '' : 's'}</b> · {labels.join(', ')}
            </>
          )}
        </span>
      )}
      {error && (
        <span role="alert" className="text-danger">
          {error}
        </span>
      )}
      {!prepared && (
        <Button type="button" variant="outline" size="sm" onClick={onDiscard}>
          Discard
        </Button>
      )}
      {!prepared && (
        <Button type="button" variant="outline" size="sm" disabled={none || busy} onClick={onReview}>
          Review changes
        </Button>
      )}
      <Button type="button" variant="default" size="sm" disabled={(none && !prepared) || busy} onClick={prepared ? onViewPrepared : onReview}>
        {prepared ? 'View hand-off' : busy ? (handoff ? 'Preparing' : 'Checking') : handoff ? 'Prepare change' : 'Save'}
      </Button>
    </div>
  )
}
