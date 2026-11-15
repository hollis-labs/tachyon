import { designConfig } from "@hollis-labs/eslint-config-design"
import tseslint from "typescript-eslint"

// Biome owns general lint/formatting; this lane enforces design-kit's token contract.
// Carried console code keeps the old tree's lanes: of src/console only agents/ was design-linted (the old
// builder lane), and packages/ never was. Tests carry fixture strings, not styling; the old lanes ignored them.
const files = ["src/**/*.{ts,tsx}"]
export default [
  {
    ignores: [
      "**/*.test.ts",
      "**/*.test.tsx",
      "src/packages/**",
      "src/console/!(agents)/**",
      "src/console/*.{ts,tsx}",
    ],
  },
  { files, languageOptions: { parser: tseslint.parser } },
  ...(await designConfig({ files })),
]
