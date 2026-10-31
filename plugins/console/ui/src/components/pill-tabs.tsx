import { Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'

export interface PillTab {
  value: string
  label: string
  count?: number
}

/**
 * Sentence-case tab row: the selected tab is a filled pill, the rest plain text,
 * counts in muted mono. Built on the kit's Tabs for keyboard and selection semantics.
 * `size="sm"` is the secondary (filter) row.
 */
export function PillTabs({ label, value, onChange, items, size = 'md', className = '' }: {
  label: string
  value: string
  onChange: (value: string) => void
  items: readonly PillTab[]
  size?: 'md' | 'sm'
  className?: string
}) {
  return (
    <Tabs value={value} onValueChange={onChange} className={`min-w-0 ${className}`}>
      <TabsList variant="line" aria-label={label} className="group-data-horizontal/tabs:h-auto w-full max-w-full flex-nowrap justify-start gap-1 overflow-x-auto p-0">
        {items.map((it) => (
          <TabsTrigger
            key={it.value}
            value={it.value}
            className={`h-auto flex-none rounded-full px-3 py-1 font-normal normal-case ${size === 'sm' ? 'dash-label' : 'dash-control'} text-fg-muted after:hidden data-active:!bg-surface-hover data-active:!text-fg dark:data-active:!bg-surface-hover`}
          >
            {it.label}
            {it.count !== undefined && <span className="font-mono dash-caption tabular-nums text-fg-muted">{it.count}</span>}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}
