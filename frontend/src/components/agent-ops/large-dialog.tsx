import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@hollis-labs/sysop-ui"
import type { ReactNode } from "react"

interface LargeDialogProps {
  open: boolean
  onClose: () => void
  title: ReactNode
  description?: ReactNode
  meta?: ReactNode
  footer?: ReactNode
  children: ReactNode
}

// FormDialog/DetailDialog (sysop-ui) hardcode height at 450px with no
// override prop — fine for a short form, too small for a tabbed view with
// a 475-row tool checklist and a reflexes table. Built directly on the
// Dialog primitives instead, sized to 80% of the viewport in both
// dimensions (falling back to the same "100% minus a margin" clamp the
// kit's own dialogs use, so it never touches the viewport edge).
export function LargeDialog({
  open,
  onClose,
  title,
  description,
  meta,
  footer,
  children,
}: LargeDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(next: boolean) => !next && onClose()}>
      <DialogContent
        widthClassName="max-w-[80vw]"
        className="flex h-[80vh] max-h-[calc(100vh-2rem)] w-[80vw] flex-col gap-0 overflow-hidden p-0"
      >
        <DialogHeader className="shrink-0 gap-1 border-b border-border-strong px-4 py-3 pr-10">
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
          {meta}
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">{children}</div>
        {footer && (
          <div className="flex h-14 shrink-0 items-center justify-end gap-2 border-t border-border-strong px-4">
            {footer}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
