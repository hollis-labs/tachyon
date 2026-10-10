import {
  createContext,
  type PropsWithChildren,
  useContext,
  useEffect,
  useSyncExternalStore,
} from "react"
import { parseLocation, type RouteLocation } from "./routes"

// pushState keeps navigation synchronous; native back/forward publishes the
// committed hash through popstate/hashchange, never through a parallel useState.
export function createHashStore(browser: Window) {
  const listeners = new Set<() => void>()
  const publish = () => {
    for (const listener of listeners) listener()
  }
  return {
    snapshot: () => browser.location.hash,
    subscribe(listener: () => void) {
      listeners.add(listener)
      if (listeners.size === 1) {
        browser.addEventListener("hashchange", publish)
        browser.addEventListener("popstate", publish)
      }
      return () => {
        listeners.delete(listener)
        if (!listeners.size) {
          browser.removeEventListener("hashchange", publish)
          browser.removeEventListener("popstate", publish)
        }
      }
    },
    navigate(path: string, replace = false) {
      if (!parseLocation(path).valid || !path.startsWith("/"))
        throw new Error("Invalid route address")
      if (browser.location.hash === `#${path}`) return
      const url = `${browser.location.pathname}${browser.location.search}#${path}`
      browser.history[replace ? "replaceState" : "pushState"](null, "", url)
      publish()
    },
  }
}

let store: ReturnType<typeof createHashStore> | undefined
function browserStore() {
  store ??= createHashStore(window)
  return store
}
export function useHashRoute() {
  const store = browserStore()
  const hash = useSyncExternalStore(store.subscribe, store.snapshot, () => "")
  useEffect(() => {
    if (!store.snapshot()) store.navigate("/agents", true)
  }, [store])
  return { ...parseLocation(hash), navigate: store.navigate }
}

export interface PageRoute extends RouteLocation {
  params: Record<string, string>
  navigate(path: string, replace?: boolean): void
}
const RouteContext = createContext<PageRoute | null>(null)
export function PageRouteProvider({ route, children }: PropsWithChildren<{ route: PageRoute }>) {
  return <RouteContext.Provider value={route}>{children}</RouteContext.Provider>
}
export function usePageRoute() {
  const route = useContext(RouteContext)
  if (!route) throw new Error("Page route provider missing")
  return route
}

export function useRouteSelection(key: string) {
  const route = usePageRoute()
  function select(value: string | null) {
    const query = new URLSearchParams(route.query)
    if (value === null) query.delete(key)
    else query.set(key, value)
    const suffix = query.toString()
    route.navigate(`${route.path}${suffix ? `?${suffix}` : ""}`)
  }
  return [route.query.get(key), select] as const
}
