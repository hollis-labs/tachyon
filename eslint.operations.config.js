import { createRequire } from 'node:module'
import { designConfig } from '@hollis-labs/eslint-config-design'
import tseslint from 'typescript-eslint'

const require = createRequire(import.meta.url)
const files = [
  'plugins/console/ui/src/transcripts/{page,detail}.tsx',
  'plugins/console/ui/src/approvals/{page,dialog}.tsx',
  'plugins/console/ui/src/forge/inbox/{PendingPrInbox,AssignDialog,PrPreviewDrawer}.tsx',
  'plugins/console/ui/src/forge/workspace/{PrWorkspace,PrWorkspacePage,MergePanel,ReviewPanel,Comments,FileTree}.tsx',
  'plugins/console/ui/src/components/{chip,pill-tabs,segment-meter,initials-avatar}.tsx',
]
export default [
  { files, languageOptions: { parser: tseslint.parser } },
  ...(await designConfig({ files, idiomManifests: [require.resolve('@hollis-labs/kit-dashboard/idiom-tokens.json')] })),
]
