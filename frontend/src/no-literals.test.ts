import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// L-E: colours come from tokens, so both modes work. No hex or rgb()/rgba() literals in app source, and no
// fixed palette utilities (text-white, bg-black, bg-zinc-900 ...). Shadows are rgb(0 0 0 / calc(a * var(--sh))), scrims
// are color-mix of var(--bg). Exceptions are listed here, with the reason.
const ROOT = resolve(__dirname, '../..')
const ALLOW: Record<string, string> = {
  // The HTML preview iframe renders someone else's page: it needs a white page in both modes.
  'plugins/console/ui/src/files/FileViewerPane.tsx': 'sandboxed HTML preview background',
}
const SKIP_DIR = /(^|\/)(node_modules|dist|fixtures|testkit|__snapshots__)(\/|$)/
const SKIP_FILE = /\.(test|spec)\.(ts|tsx)$/

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const rel = relative(ROOT, p)
    if (SKIP_DIR.test(rel)) continue
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(ts|tsx|css)$/.test(name) && !SKIP_FILE.test(name)) out.push(p)
  }
  return out
}

const HEX = /(?<![\w&])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b(?![\w-])/
const FN = /\brgba?\(\s*\d/
const PALETTE = /\b(?:text|bg|border|ring|fill|stroke|from|to|via|shadow|outline|decoration)-(?:white|black)(?:\/\d+)?\b|\b(?:text|bg|border|ring|fill|stroke)-(?:zinc|gray|slate|neutral|stone|red|green|blue|amber|yellow|orange|emerald|sky|indigo|violet|purple|pink|rose|cyan|teal|lime)-\d{2,3}\b/

describe('no colour literals in app source (light mode L-E)', () => {
  it('uses tokens only', () => {
    const roots = ['frontend/src', 'plugins', 'packages'].map((r) => join(ROOT, r)).filter((p) => { try { return statSync(p).isDirectory() } catch { return false } })
    const bad: string[] = []
    for (const f of roots.flatMap((r) => walk(r))) {
      const rel = relative(ROOT, f)
      if (ALLOW[rel]) continue
      const lines = readFileSync(f, 'utf8').split('\n')
      lines.forEach((line, i) => {
        const code = line.replace(/\/\/.*$/, '').replace(/\/\*.*?\*\//g, '')
        if (/^\s*\*/.test(line)) return
        if (HEX.test(code) || FN.test(code) || PALETTE.test(code)) bad.push(`${rel}:${i + 1}: ${line.trim().slice(0, 120)}`)
      })
    }
    expect(bad).toEqual([])
  })
})
