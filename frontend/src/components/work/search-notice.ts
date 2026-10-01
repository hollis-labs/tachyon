import type { WorkList } from "../../api/client"

// Both provider wire shapes are normalized to WorkList by work-ops. Never
// infer an exact total from the returned page or a continuation flag alone.
export function workSearchNotice(page: WorkList): string | undefined {
  if (
    !Array.isArray(page.tasks) ||
    page.tasks.length === 0 ||
    page.tasks.length > 200 ||
    typeof page.has_more !== "boolean" ||
    !Number.isSafeInteger(page.total) ||
    page.total <= page.tasks.length
  )
    return
  return `Showing the first ${page.tasks.length} of ${page.total} matches. Refine your search.`
}
