import { PageHeader } from "@hollis-labs/kit-dashboard"

export function NotAvailablePage({
  path,
  detail,
  reason,
}: {
  path: string
  detail: string
  reason?: string
}) {
  return (
    <section className="flex flex-col" data-route-reason={reason}>
      <PageHeader title="Page not available" />
      <div className="space-y-3 p-4">
        <p role="status">{detail}</p>
        <p className="break-all font-mono text-sm">{path}</p>
        <a className="underline" href="#/dashboard">
          Open Dashboard
        </a>
      </div>
    </section>
  )
}
