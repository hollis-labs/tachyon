import type { TachyonPlugin } from "@tachyon/host-sdk"
import { PlannerApiProvider } from "@planner/api/context"
import { TachyonPlannerClient } from "@planner/api/client"
import { Compass } from "lucide-react"
import PlannerPage from "@planner/pages/PlannerPage"

/** Planner records and revisions are owned by the Tachyon host. */
const plannerClient = new TachyonPlannerClient()

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
