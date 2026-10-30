// Small initials disc for people and agents where no image exists.
const initials = (name: string) => {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean)
  return ((parts[0]?.[0] ?? '?') + (parts.length > 1 ? parts[parts.length - 1][0] : '')).toUpperCase()
}

export function InitialsAvatar({ name, className = '' }: { name: string; className?: string }) {
  return (
    <span aria-hidden className={`inline-flex size-5 shrink-0 items-center justify-center rounded-full border border-border bg-surface font-mono dash-caption text-fg-muted ${className}`}>
      {initials(name)}
    </span>
  )
}
