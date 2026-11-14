/**
 * The one fetch wrapper for Tachyon's own API (next/docs/api-contract.md).
 *
 * - Same-origin cookie session; every request carries `X-Requested-With`, which the backend requires
 *   on cookie writes.
 * - Errors are the flat `{code, message, details?}` body. `ApiError` keeps all three and the status.
 * - A 401 means the session ended: `UNAUTHORIZED_EVENT` fires and the identity gate returns to sign-in.
 *   403 carries `scope_required` (with `details.scope`) or `operator_required`; callers branch on `code`.
 * - Sign-in, setup and the identity screens are kit-identity's own and do not use this wrapper.
 */

export interface ErrorBody {
  code?: string
  message?: string
  details?: Record<string, unknown>
}

/** Raised for any non-2xx answer. `code` is the flat error code, when the body carried one. */
export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code = "",
    public readonly details: Record<string, unknown> | undefined = undefined,
  ) {
    super(message)
    this.name = "ApiError"
  }
}

/** Fired on a 401: the session ended. The identity gate returns to its sign-in screen. */
export const UNAUTHORIZED_EVENT = "tachyon:unauthorized"

/** The value next/docs/api-contract.md section 2 names; the identity middleware requires the header on writes. */
export const REQUESTED_WITH = "tachyon-ui"
export const GENERIC_MESSAGE = "The request could not be completed."

export interface RequestOptions {
  method?: string
  body?: unknown
  headers?: Record<string, string>
  signal?: AbortSignal
  /** Keep the request alive across a page unload (replaces navigator.sendBeacon, which cannot set headers). */
  keepalive?: boolean
}

/** Parses a response body as the flat error shape. Anything else yields an empty object. */
export function parseErrorBody(text: string): ErrorBody {
  if (!text) return {}
  try {
    const data: unknown = JSON.parse(text)
    if (!data || typeof data !== "object" || Array.isArray(data)) return {}
    const { code, message, details } = data as Record<string, unknown>
    return {
      code: typeof code === "string" ? code : undefined,
      message: typeof message === "string" ? message : undefined,
      details:
        details && typeof details === "object" && !Array.isArray(details)
          ? (details as Record<string, unknown>)
          : undefined,
    }
  } catch {
    return {}
  }
}

/**
 * JSON over the same-origin session. `path` is absolute (`/api/...`). An empty body (204) resolves
 * to undefined.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, headers, signal, keepalive } = options
  const response = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: {
      Accept: "application/json",
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      "X-Requested-With": REQUESTED_WITH,
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
    keepalive,
  })
  const text = await response.text()
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    const err = parseErrorBody(text)
    throw new ApiError(err.message || GENERIC_MESSAGE, response.status, err.code ?? "", err.details)
  }
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(
      "The server sent a response that is not JSON.",
      response.status,
      "invalid_response",
    )
  }
}
