import { applyTheme, DEFAULT_THEME } from "@hollis-labs/kit-dashboard"
import React from "react"
import ReactDOM from "react-dom/client"
import { App } from "./App"
import { ApiProvider } from "./api/context"
import { initMode } from "./mode"
import "./index.css"

// One palette; colour mode is the user choice. index.html set both before first paint.
applyTheme(DEFAULT_THEME)
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
