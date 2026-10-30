import { applyTheme } from "@hollis-labs/kit-dashboard"
import React from "react"
import ReactDOM from "react-dom/client"
import { App } from "./App"
import { ApiProvider } from "./api/context"
import { initMode, NANITE_DEFAULT } from "./mode"
import "./index.css"

// One palette; colour mode is the user choice. index.html set both before first paint.
// The kit's ThemeName union predates nanite-default; the value is valid at runtime.
applyTheme(NANITE_DEFAULT as Parameters<typeof applyTheme>[0])
initMode()

const root = document.getElementById("root")
if (!root) {
  throw new Error("root element #root not found")
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <ApiProvider>
      <App />
    </ApiProvider>
  </React.StrictMode>,
)
