import type { RefKind } from '../types'

/** Approval item ids as Tachyon mints them (`item_` + 32 hex). */
export const APPROVAL_ITEM_ID = /^item_[0-9a-f]{32}$/

// Only named through a `tach:approval/<item_id>` link an agent was handed (for
// example by the actions.request tool); free text is never scanned for these.
export function createApprovalKind(): RefKind {
  return {
    id: 'approval',
    label: 'Approval',
    icon: 'approval',
    priority: 85,
    scan: () => [],
    chipLabel: () => 'Approval',
    // The approvals page opens the review dialog (and its terminal) for ?item=.
    open: (ref) => ({ type: 'route', to: `/approvals?item=${encodeURIComponent(ref.canonical)}` }),
  }
}

// `tach:card/<item_id>` names a card the same way; cards live in the same queue
// and open in the same review dialog.
export function createCardKind(): RefKind {
  return {
    id: 'card',
    label: 'Card',
    icon: 'approval',
    priority: 85,
    scan: () => [],
    chipLabel: () => 'Card',
    open: (ref) => ({ type: 'route', to: `/approvals?item=${encodeURIComponent(ref.canonical)}` }),
  }
}
