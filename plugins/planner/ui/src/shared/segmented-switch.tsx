import { Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'

export interface SegmentedSwitchProps {
  label: string
  value: string
  onChange: (value: string) => void
  items: readonly { value: string; label: string }[]
}

/** Two or three views of the same item, side by side in one outlined control (Board / Timeline). */
export function SegmentedSwitch({ label, value, onChange, items }: SegmentedSwitchProps) {
  return (
    <Tabs value={value} onValueChange={onChange} className="gap-0">
      <TabsList variant="line" aria-label={label} className="h-6.5! gap-0 overflow-hidden rounded-control border border-border p-0 group-data-horizontal/tabs:h-6.5">
        {items.map((it) => (
          <TabsTrigger key={it.value} value={it.value} className="h-full flex-none rounded-none border-0 px-3 text-xs font-normal text-fg-muted after:hidden hover:text-fg data-active:bg-selection data-active:font-semibold data-active:text-fg">
            {it.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}
