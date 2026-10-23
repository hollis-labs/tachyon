import { createRequire } from 'node:module'
import { designConfig } from '@hollis-labs/eslint-config-design'
import tseslint from 'typescript-eslint'

const require = createRequire(import.meta.url)
// The gate grows with the migration: each PR adds the directories it migrated, severity error.
const files = ['plugins/console/ui/src/alerts/**/*.{ts,tsx}', 'plugins/console/ui/src/cards/**/*.{ts,tsx}']
// Some migrated files carry `eslint-disable react-hooks/exhaustive-deps` for Biome's sibling rule; this lane does not
// load that plugin, so give the directive a rule to point at instead of failing on an unknown definition.
const noopRule = { meta: { schema: [] }, create: () => ({}) }
const hooksDirectives = { plugins: { 'react-hooks': { rules: { 'exhaustive-deps': noopRule, purity: noopRule } } } }
export default [
  { linterOptions: { reportUnusedDisableDirectives: 'off' } },
  { ignores: ['**/*.test.{ts,tsx}'] },
  { files, languageOptions: { parser: tseslint.parser } },
  { files, ...hooksDirectives },
  ...(await designConfig({ files, idiomManifests: [require.resolve('@hollis-labs/kit-dashboard/idiom-tokens.json')] })),
]
