import type { TachyonPlugin } from "@tachyon/host-sdk"
import { Bot, Hammer } from "lucide-react"

function ConsolePlaceholder() {
  return <div className="p-6 text-sm text-muted-foreground">Console port in progress.</div>
}

export const consolePlugin: TachyonPlugin = {
  id: "console",
  name: "Console",
  nav: [
    { key: "agents", label: "Agents", to: "/agents", icon: <Bot className="h-4 w-4" />, priority: 20 },
    { key: "builder", label: "Builder", to: "/builder", icon: <Hammer className="h-4 w-4" />, priority: 30 },
  ],
  routes: [
    { path: "/agents", element: <ConsolePlaceholder /> },
    { path: "/builder", element: <ConsolePlaceholder /> },
  ],
}
