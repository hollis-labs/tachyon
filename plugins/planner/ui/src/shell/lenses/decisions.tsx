import { usePlannerApi } from '@planner/api/context'
import { TEXT } from '@planner/lib/vocabulary'
import { OptionCompare } from '@planner/shared'
import type { PlannerItem } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

export function DecisionBody({ item, write, busy }: { item: PlannerItem; write: Write; busy: boolean }) {
  const api = usePlannerApi()
  const options = item.options ?? []
  const chosen = typeof item.chosen === 'string' ? item.chosen : typeof item.decision === 'string' ? item.decision : item.status === 'decided' ? '' : undefined
  const addOption = (d: { label: string; pros: string; cons: string }) => {
    const used = new Set(options.map((o) => o.id))
    let id = String.fromCharCode(97 + options.length)
    while (used.has(id)) id += 'x'
    return write(() => api.plannerUpdate('decisions', item.id, { options: [...options, { id, label: d.label.trim(), ...(d.pros.trim() && { pros: d.pros.trim() }), ...(d.cons.trim() && { cons: d.cons.trim() }) }] }, item.rev))
  }
  const comment = () => document.querySelector<HTMLTextAreaElement>(`textarea[aria-label="${TEXT.commentPlaceholder}"]`)?.focus()
  if (options.length === 0) return <p className="text-control text-fg-muted">{TEXT.noOptions}</p>
  return (
    <OptionCompare
      options={options}
      chosenId={chosen || undefined}
      onChoose={(opt) => void write(() => api.plannerDecide(item.id, opt, item.rev))}
      onComment={comment}
      onAdd={addOption}
      busy={busy}
      labels={{ choose: TEXT.chooseOption, chooseButton: TEXT.chooseButton, recommended: TEXT.recommended, chosen: TEXT.chosen, pros: TEXT.pros, cons: TEXT.cons, groupLabel: TEXT.optionsGroup, comment: TEXT.commentVerb, addOption: TEXT.addOption, addLabel: TEXT.addOptionLabel, addPros: TEXT.pros, addCons: TEXT.cons, addSave: TEXT.addOptionSave, footNote: TEXT.chooseNote }}
    />
  )
}
