import { designConfig } from '@hollis-labs/eslint-config-design'
import tseslint from 'typescript-eslint'

const files = ['plugins/console/ui/src/agents/**/*.{ts,tsx}', 'plugins/console/ui/src/objects/**/*.{ts,tsx}']
export default [
  { ignores: ['**/*.test.ts', '**/*.test.tsx'] },
  { files, languageOptions: { parser: tseslint.parser } },
  ...(await designConfig({ files })),
]
