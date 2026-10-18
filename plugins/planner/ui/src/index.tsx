import type { TachyonPlugin } from "@tachyon/host-sdk"
import { PlannerApiProvider } from "@planner/api/context"
import { TachyonPlannerClient } from "@planner/api/client"
import { createLegacyPlannerClient } from "@planner/api/legacy"
import { Compass } from "lucide-react"
import PlannerPage from "@planner/pages/PlannerPage"

/** Explicit verification build only. The deployed default stays on the legacy adapter. */
const plannerClient = import.meta.env.VITE_TACHYON_PLANNER === "verify"
  ? new TachyonPlannerClient()
  : createLegacyPlannerClient()

function PlannerRoute() {
  return (
    <PlannerApiProvider client={plannerClient}>
      <PlannerPage />
    </PlannerApiProvider>
  )
}

export const plannerPlugin: TachyonPlugin = {
  id: "planner",
  name: "Planner",
  nav: [{ key: "planner", label: "Planner", to: "/planner", icon: <Compass className="h-4 w-4" />, priority: 10 }],
  routes: [
    { path: "/planner", element: <PlannerRoute /> },
    { path: "/planner/:db", element: <PlannerRoute /> },
    { path: "/planner/:db/:id", element: <PlannerRoute /> },
  ],
}
