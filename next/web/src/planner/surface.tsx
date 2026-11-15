import { TachyonPlannerClient } from "@planner/api/client"
import { PlannerApiProvider } from "@planner/api/context"
import { lensOfSlug, lensPath } from "@planner/lib/vocabulary"
import LensPage from "@planner/shell/lens-page"
import PlannerShell from "@planner/shell/PlannerShell"
import { Navigate, Route, Routes, useParams } from "react-router-dom"

/** Planner records and revisions are owned by the Tachyon backend. */
const plannerClient = new TachyonPlannerClient()

function ShellRoute() {
  return (
    <PlannerApiProvider client={plannerClient}>
      <PlannerShell />
    </PlannerApiProvider>
  )
}

/** The old plugin's route, unchanged: lens slugs, legacy slugs and the database-name alias. */
function PlannerRoute() {
  const { db: slug, id } = useParams<{ db?: string; id?: string }>()
  // The old pages' routes land on the lens or facet that replaced them.
  if (slug === undefined || slug === "board" || slug === "flight" || slug === "priorities")
    return <Navigate to="/planner/themes" replace />
  if (slug === "activity") return <Navigate to="/planner/themes?facet=need-you" replace />
  const db = lensOfSlug(slug)
  if (!db) return <Navigate to="/planner/themes" replace />
  // The database's own name (workstreams) is an alias of the lens slug (streams).
  if (slug !== lensPath(db).slice("/planner/".length))
    return (
      <Navigate to={lensPath(db, id === undefined ? undefined : decodeURIComponent(id))} replace />
    )
  return (
    <PlannerApiProvider client={plannerClient}>
      {db === "themes" ? <PlannerShell /> : <LensPage key={db} db={db} />}
    </PlannerApiProvider>
  )
}

/**
 * The planner surface (`/planner` in ui/app.json). The shell picks this page for every `/planner/...` path
 * (surfaces.ts); the page resolves the rest itself with the old plugin's route table, so every deep link and
 * legacy slug keeps working.
 */
export function PlannerSurface() {
  return (
    <Routes>
      <Route path="/planner/themes" element={<ShellRoute />} />
      <Route path="/planner/themes/:id" element={<ShellRoute />} />
      <Route path="/planner" element={<PlannerRoute />} />
      <Route path="/planner/:db" element={<PlannerRoute />} />
      <Route path="/planner/:db/:id" element={<PlannerRoute />} />
    </Routes>
  )
}
