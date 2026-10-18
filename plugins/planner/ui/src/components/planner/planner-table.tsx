import { RecordListContent, RecordListHeader } from '@hollis-labs/kit-dashboard/data'

export const PlannerRowContent = RecordListContent

export function PlannerTableHeader({ trailing = 'Comments' }: { trailing?: string }) {
  return <RecordListHeader trailing={<span title={trailing}>{trailing === 'Comments' ? 'Replies' : trailing}</span>} />
}
