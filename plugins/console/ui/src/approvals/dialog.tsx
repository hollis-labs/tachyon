import { Button, Dialog, DialogContent, DialogDescription, DialogTitle } from "@hollis-labs/design-components"
import type { ApprovalClient, ApprovalItem } from "./client"
import { DecisionDetail } from "./detail"

// The review dialog (deep links, bell): the same detail the Approvals page shows inline.
export function ApprovalDialog({ id, initial, client, onClose, onChanged }: {
  id: string
  initial?: ApprovalItem
  client: ApprovalClient
  onClose(): void
  onChanged(): void
}) {
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="dash-detail-dialog max-h-[85vh] overflow-hidden p-0 sm:max-w-2xl">
        <DialogTitle className="sr-only">Review request</DialogTitle>
        <DialogDescription className="sr-only">Review the request and record a decision.</DialogDescription>
        <DecisionDetail id={id} initial={initial} client={client} onChanged={onChanged}
          footer={<Button variant="outline" size="sm" className="font-mono dash-label" onClick={onClose}>Close</Button>} />
      </DialogContent>
    </Dialog>
  )
}
