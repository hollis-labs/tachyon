import { refreshPageData, useDataFreshness } from '@tachyon/host-sdk'
import { RefreshCw } from 'lucide-react'

export function ChatFreshness() {
  const rows = useDataFreshness().filter(
    (r) => ['chat', 'chat-turns'].includes(r.scope) && r.page === window.location.pathname,
  )
  if (!rows.length) return null
  const error = rows.some((r) => r.problem || r.state === 'error')
  const refreshing = rows.some((r) => r.state === 'refreshing')
  const cached = rows.some((r) => r.state === 'cached')
  const updated = rows.flatMap((r) => (r.updatedAt ? [r.updatedAt] : [])).sort()[0]
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 font-mono text-[10px] text-fg-faint">
      <span className={error ? 'text-warning' : 'text-fg-muted'}>
        {error
          ? 'Source unavailable'
          : refreshing
            ? 'Cached · checking for updates'
            : cached
              ? 'Cached'
              : 'Live'}
      </span>
      {updated && (
        <time dateTime={updated} title={new Date(updated).toLocaleString()}>
          Updated {new Date(updated).toLocaleTimeString()}
        </time>
      )}
      <button
        type="button"
        aria-label="Refresh chat"
        title="Refresh chat"
        onClick={() => void refreshPageData()}
        className="ml-auto p-1 hover:text-fg"
      >
        <RefreshCw className="h-3 w-3" />
      </button>
    </div>
  )
}
