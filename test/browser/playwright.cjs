// Resolve the dev-only Playwright module for every probe. CI installs it under
// tools/clientbench, which is not on the require path from test/browser.
const path = require('path')

const REPO = path.resolve(__dirname, '..', '..')
let chromium
let lastError
for (const candidate of [
  process.env.DF_PLAYWRIGHT_MODULE,
  'playwright',
  path.join(REPO, 'tools', 'clientbench', 'node_modules', 'playwright')
].filter(Boolean)) {
  try {
    ;({ chromium } = require(candidate))
    break
  } catch (error) {
    lastError = error
  }
}
if (!chromium) {
  console.error(
    'probe requires Playwright. From tools/clientbench run `npm ci` and ' +
      '`npx playwright install chromium`, or set DF_PLAYWRIGHT_MODULE to an ' +
      'installed module.'
  )
  console.error(lastError ? lastError.message : 'playwright is not resolvable')
  process.exit(1)
}
module.exports = { chromium }
