import { designConfig } from "@hollis-labs/eslint-config-design"
import tseslint from "typescript-eslint"

// Biome owns general lint/formatting; this lane enforces design-kit's token contract.
export default [
  // Tests carry fixture strings (issue refs like "owner/repo#246"), not styling; the old planner lane ignored them too.
  { ignores: ["**/*.test.ts", "**/*.test.tsx"] },
  { files: ["src/**/*.{ts,tsx}"], languageOptions: { parser: tseslint.parser } },
  ...(await designConfig({ files: ["src/**/*.{ts,tsx}"] })),
]
