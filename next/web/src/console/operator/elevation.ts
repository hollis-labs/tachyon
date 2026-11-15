import { hostFetch } from "@tachyon/host-sdk"

/**
 * The port has no step-up elevation (next/docs/api-contract.md section 5): operator is the `tachyon.operator`
 * scope the signed-in person holds, checked by the backend. The old module (status, session, elevate, prompt,
 * replay) is not carried; callers keep their `operatorFetch` import, which is now the host fetch: same-origin
 * session, X-Requested-With, and a 401 hands back to sign-in (packages/host-sdk freshness.ts).
 */
export const operatorFetch: typeof fetch = hostFetch
