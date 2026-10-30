import { cn } from '@hollis-labs/design-components'
import { Button } from '@hollis-labs/design-components'

export interface CompareOption {
  id: string
  label: string
  summary?: string
  pros?: string
  cons?: string
  recommended?: boolean
}

export interface OptionCompareLabels {
  choose: (label: string) => string
  chooseButton: string
  recommended: string
  chosen: string
  pros: string
  cons: string
  groupLabel: string
}

/** Options side by side with pros and cons. Press 1..n to focus an option, Enter on Choose commits. Once chosen the others dim. */
export function OptionCompare({ options, chosenId, onChoose, busy, labels, className }: { options: readonly CompareOption[]; chosenId?: string; onChoose: (id: string) => void; busy?: boolean; labels: OptionCompareLabels; className?: string }) {
  return (
    <div
      role="group"
      aria-label={labels.groupLabel}
      className={cn('grid gap-3 md:grid-cols-2 xl:grid-cols-3', className)}
      onKeyDown={(e) => {
        const n = Number(e.key)
        if (e.metaKey || e.ctrlKey || e.altKey || !Number.isInteger(n) || n < 1 || n > options.length) return
        if ((e.target as HTMLElement).tagName === 'INPUT' || (e.target as HTMLElement).tagName === 'TEXTAREA') return
        e.preventDefault()
        ;(e.currentTarget.querySelectorAll<HTMLElement>('[data-option]')[n - 1])?.focus()
      }}
    >
      {options.map((o, i) => {
        const dim = chosenId !== undefined && chosenId !== o.id
        return (
          <article key={o.id} data-option tabIndex={0} aria-label={`${i + 1}. ${o.label}`} className={cn('flex flex-col gap-2 rounded-md border border-border p-3 outline-none focus-visible:outline-2', dim && 'opacity-50')}>
            <header className="flex items-center gap-2">
              <kbd className="font-mono dash-caption text-fg-faint">{i + 1}</kbd>
              <h3 className="flex-1 dash-support font-medium text-fg">{o.label}</h3>
              {o.recommended && <span className="dash-caption text-fg-muted">{labels.recommended}</span>}
              {chosenId === o.id && <span className="dash-caption text-fg">{labels.chosen}</span>}
            </header>
            {o.summary && <p className="dash-support text-fg-muted">{o.summary}</p>}
            {o.pros && <p className="dash-support text-fg"><span className="mr-1 dash-caption text-fg-faint">{labels.pros}</span>{o.pros}</p>}
            {o.cons && <p className="dash-support text-fg"><span className="mr-1 dash-caption text-fg-faint">{labels.cons}</span>{o.cons}</p>}
            {chosenId === undefined && (
              <Button size="sm" variant="outline" disabled={busy} aria-label={labels.choose(o.label)} onClick={() => onChoose(o.id)} className="self-start">
                {labels.chooseButton}
              </Button>
            )}
          </article>
        )
      })}
    </div>
  )
}
