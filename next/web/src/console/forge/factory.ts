import type { ForgeClient } from './client'
import { currentConfirmToken } from './confirm-token'
import { HttpForgeClient, type HttpForgeClientOptions } from './http'

export interface CreateForgeClientOptions {
  /** Base URL override (else VITE_FORGE_BASE_URL, else the console plugin's forge base). */
  baseUrl?: string
  http?: Omit<HttpForgeClientOptions, 'baseUrl'>
}

/** Tachyon has no forge mock: the client is always the HTTP one (missing routes surface as errors). */
export function createForgeClient(opts: CreateForgeClientOptions = {}): ForgeClient {
  return new HttpForgeClient({ ...opts.http, baseUrl: opts.baseUrl })
}

/** Browser convenience: reads the Vite env base override and wires the step-up token. */
export function createDefaultForgeClient(): ForgeClient {
  return createForgeClient({
    baseUrl: import.meta.env.VITE_FORGE_BASE_URL as string | undefined,
    http: { confirmToken: currentConfirmToken },
  })
}
