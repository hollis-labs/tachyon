import type { TachyonPlugin } from "@tachyon/host-sdk"
import { ApiProvider, createPluginTorqueClient } from "@tachyon/torque-client"
import { Compass } from "lucide-react"
import PlannerPage from "@planner/pages/PlannerPage"

/** Planner API: buffered calls via /api/plugins/planner/v1, SSE via the host relay. */
const plannerClient = createPluginTorqueClient("planner", ["events"])

function PlannerRoute() {
  return (
    <ApiProvider client={plannerClient}>
      <PlannerPage />
    </ApiProvider>
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
