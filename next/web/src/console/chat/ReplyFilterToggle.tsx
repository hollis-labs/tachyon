import { Button } from '@hollis-labs/design-components'
import { User, Users } from 'lucide-react'
import type { ReplyFilter } from './replyFilter'

/** Icon toggle between every agent reply and only the replies addressed to the operator. */
export function ReplyFilterToggle({ value, onChange }: { value: ReplyFilter; onChange: (next: ReplyFilter) => void }) {
  const mine = value === 'mine'
  const Icon = mine ? User : Users
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-pressed={mine}
      aria-label="Only show replies to me"
      title={mine ? 'Hiding replies to other agents. Select to show all replies.' : 'Showing all replies. Select to hide replies to other agents.'}
      onClick={() => onChange(mine ? 'all' : 'mine')}
      className={`gap-1.5 px-2 font-mono dash-caption ${mine ? 'text-primary' : 'text-fg-muted'}`}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      <span>{mine ? 'Replies to me' : 'All replies'}</span>
    </Button>
  )
}
