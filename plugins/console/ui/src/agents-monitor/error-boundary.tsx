import { Component, type ErrorInfo, type ReactNode } from 'react'
import { EmptyState } from '@hollis-labs/design-components'
import { AGENTS } from './vocabulary'

interface Props {
  /** Changes when the thing shown changes (the open session): a new key clears an old error. */
  resetKey?: string
  /** Drawn as the error state; gets Close and Retry handlers. */
  children: ReactNode
  onClose?: () => void
}

/**
 * A bad payload must never blank the page. Whatever throws below is replaced by an error state with Retry
 * (and Close when the boundary wraps the session modal); the rest of the page keeps working.
 */
export class AgentsErrorBoundary extends Component<Props, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('agents: view failed', error, info.componentStack)
  }
  componentDidUpdate(prev: Props) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null })
  }
  render() {
    if (!this.state.error) return this.props.children
    const { onClose } = this.props
    return (
      <div role="alert" className={onClose ? 'fixed inset-0 z-[70] grid place-items-center bg-black/40 p-4' : undefined}>
        <div className={onClose ? 'w-full max-w-md rounded-panel border border-border bg-bg-elevated p-4' : undefined}>
          <EmptyState
            variant="error"
            title={AGENTS.crashed}
            description={AGENTS.crashedDetail}
            action={{ label: AGENTS.retry, onClick: () => this.setState({ error: null }) }}
          />
          {onClose && <div className="mt-2 flex justify-center"><button type="button" onClick={onClose} className="text-xs text-fg-secondary underline">{AGENTS.modal.close}</button></div>}
        </div>
      </div>
    )
  }
}
