import { EmptyState } from "@hollis-labs/design-components"

/** A surface whose content arrives in a later slice (G2+). It shows the path it was reached with. */
export function StubPage({ area, slice }: { area: string; slice: string }) {
  return (
    <div className="page-content" data-stub={area}>
      <EmptyState
        variant="empty"
        title={`${area} is being ported`}
        description={`This surface arrives in ${slice}. Path: ${window.location.pathname}`}
      />
    </div>
  )
}
