import { useState } from 'react'
import { Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@hollis-labs/design-components'

/** Discard asks only when something would be lost; the kit dialog replaces the browser confirm. The description says what happens to the edits on this page. */
export function useDiscardGuard(description = 'Your edits have not been sent for review and will be lost.') {
  const [pending, setPending] = useState<{ n: number; run: () => void }>()
  const guard = (n: number, run: () => void) => (n === 0 ? run() : setPending({ n, run }))
  const dialog = pending && (
    <Dialog open onOpenChange={(o) => !o && setPending(undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Discard {pending.n} change{pending.n === 1 ? '' : 's'}?
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="outline" size="sm" onClick={() => setPending(undefined)}>
            Keep editing
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            onClick={() => {
              const run = pending.run
              setPending(undefined)
              run()
            }}
          >
            Discard
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
  return { guard, dialog }
}
