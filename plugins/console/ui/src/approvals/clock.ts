// Countdowns follow the server's clock: items carry `server_now_ms`, and the difference to this
// browser's clock at that moment is remembered, so a wrong local clock cannot show an expired
// request as live or the reverse.
let skewMs = 0

/** Record the server's "now" as seen at this moment. A missing or non-numeric value changes nothing. */
export function noteServerNow(serverNowMs: unknown, localNow = Date.now()) {
  if (typeof serverNowMs === "number" && Number.isFinite(serverNowMs)) skewMs = serverNowMs - localNow
}

/** The current time on the server's clock. */
export const serverNow = () => Date.now() + skewMs
