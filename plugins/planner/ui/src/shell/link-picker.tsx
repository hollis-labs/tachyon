import { useEffect } from 'react'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@hollis-labs/design-components'
import { LINK_KINDS, TEXT, type LinkKind } from '@planner/lib/vocabulary'

/** Asks how two items relate: four kinds, number keys 1..4. Esc cancels (and the dialog returns focus). */
export function LinkKindPicker({ open, from, to, onPick, onCancel }: { open: boolean; from: string; to: string; onPick: (k: LinkKind) => void; onCancel: () => void }) {
  useEffect(() => {
    if (!open) return
    const h = (e: KeyboardEvent) => {
      const k = LINK_KINDS[Number(e.key) - 1]
      if (k) (e.preventDefault(), onPick(k.id))
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, onPick])
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent widthClassName="max-w-sm">
        <DialogHeader>
          <DialogTitle>{TEXT.linkKindTitle}</DialogTitle>
          <DialogDescription>{from} → {to}. {TEXT.linkKindHint}</DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col gap-1">
          {LINK_KINDS.map((k, i) => (
            <li key={k.id}>
              <button type="button" onClick={() => onPick(k.id)} className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left dash-support text-fg hover:bg-panel-hover">
                <span className="flex-1">{k.label}</span>
                <kbd className="font-mono dash-caption text-fg-faint">{i + 1}</kbd>
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
