// The bell as an activity list (UI-34), driven by a fake OpenCode API.
//
//   test/browser/probe.sh activity
//
// The bug this covers: the bell was a disabled grey icon and an OpenCode
// question never reached it, so clicking did nothing at all. The probe starts
// the daemon against test/browser/fixture-opencode-api.py, which serves one chat
// and one pending question form, and asserts that:
//
//  - the bell is clickable with nothing unread and explains its empty state;
//  - a question that was already waiting when the page loaded still reaches the
//    ticker (the daemon's baseline offers open prompts);
//  - the row names the chat and the workspace it came from;
//  - following the row opens the workspace's terminal session, not the chat;
//  - the event stays until the owner dismisses it, and does not come back from
//    a reload once the question is answered.
//
// Exit codes match the other probes: 1 precondition, 2 setup, 3 regression.
const fs = require('fs')
const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const STUB_FORMS = process.env.DFB_STUB_FORMS

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

async function state() {
  const response = await fetch(`${BASE}/api/v1/state`, {
    headers: { Authorization: `Bearer ${TOKEN}` }
  })
  if (!response.ok) throw new Error(`state ${response.status}`)
  return response.json()
}

async function signIn(page) {
  await page.goto(`${BASE}/?debug=1&panel=0`)
  // A reload keeps the device key, so the token form is only for the first load.
  try {
    await page.waitForSelector('#app:not([hidden])', { timeout: 4000 })
    return
  } catch {}
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
}

async function openBell(page) {
  await page.click('#btn-attention')
  await page.waitForSelector('#attention-pop:not([hidden])', { timeout: 5000 })
}

async function main() {
  const browser = await chromium.launch({ args: ['--no-sandbox'] })
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  const page = await context.newPage()
  try {
    await signIn(page)
    if (!STUB_FORMS) fail(1, 'DFB_STUB_FORMS is not set; run this through probe.sh')

    // The bell is a button before it has anything to say.
    if (await page.locator('#btn-attention').isDisabled()) {
      fail(3, 'the bell is disabled while a question is waiting')
    }

    // The question was already pending when the daemon started, so it can only
    // arrive through the baseline. Before the fix this stayed at zero forever.
    try {
      await page.waitForSelector('#attention-count:not([hidden])', { timeout: 20000 })
    } catch {
      fail(3, 'a pending OpenCode question never reached the bell')
    }

    const workspace = (await state()).worktrees.find((wt) => wt.path)
    const workspaceName = workspace
      ? (workspace.branch || workspace.path.split('/').filter(Boolean).pop() || '')
      : ''

    await openBell(page)
    const rowText = (await page.locator('#attention-list .attention-go').first().textContent()) || ''
    if (!rowText.includes('Question for you') || !rowText.includes('Which database should I use?')) {
      fail(3, `the activity row does not describe the question: ${JSON.stringify(rowText)}`)
    }

    // The daemon polls OpenCode every ten seconds, and this probe adds its repo
    // just after the daemon starts, so the directory-to-workspace link can land
    // on the next poll. Reopening the list takes a fresh baseline, which is how
    // an already-delivered row picks the link up.
    let source = ''
    const linkDeadline = Date.now() + 20000
    while (Date.now() < linkDeadline) {
      source = (await page.locator('#attention-list .attention-row-source').first().textContent()) || ''
      if (source.includes(workspaceName)) break
      await page.keyboard.press('Escape')
      await sleep(1200)
      await openBell(page)
    }
    if (!source.includes('Chat Q')) {
      fail(3, `the activity row does not name its chat: ${JSON.stringify(source)}`)
    }
    if (!workspaceName || !source.includes(workspaceName)) {
      // The label is the branch, or the directory name when the row is the
      // adopted checkout. A miss means the directory-to-worktree mapping failed.
      fail(3, `the activity row does not name its workspace ${JSON.stringify(workspaceName)}: ${JSON.stringify(source)}`)
    }
    await page.screenshot({ path: `${process.env.DFB_ROOT}/activity-question.png` })

    // Following the row opens the workspace's session page (this fixture runs
    // no PTY, so the fallback is the worktree view), and the event stays
    // unread.
    await page.locator('#attention-list .attention-go').first().click()
    await page.waitForSelector('#app[data-view="worktree"]', { timeout: 15000 })
    if (!(await page.locator('#attention-count').isVisible())) {
      fail(3, 'opening the session silently cleared the activity')
    }

    // A reload while the question is still waiting must keep it: it is open,
    // not history.
    await signIn(page)
    try {
      await page.waitForSelector('#attention-count:not([hidden])', { timeout: 20000 })
    } catch {
      fail(3, 'a still-waiting question disappeared after a reload')
    }

    // Answering it removes it from the daemon's open set. The page keeps its
    // unread row until the owner dismisses it; that is the manual-clear contract.
    fs.writeFileSync(STUB_FORMS, '{"data":[]}')
    await sleep(4000)
    await openBell(page)
    await page.locator('#attention-list .attention-dismiss').first().click()
    await sleep(300)
    if (await page.locator('#attention-count').isVisible()) {
      fail(3, 'dismissing the row did not clear the bell')
    }

    // The dismiss reached the daemon's ring, so a reload starts clean.
    await signIn(page)
    await sleep(2500)
    if (await page.locator('#attention-count').isVisible()) {
      fail(3, 'an answered question came back after a reload')
    }
    if (await page.locator('#btn-attention').isDisabled()) {
      fail(3, 'the bell is disabled after the list is cleared')
    }

    // The owner's test case: a normal "good morning" answer asks nothing, but it
    // is still activity. Before this it raised no event at all, so the list
    // looked permanently blank.
    fs.writeFileSync(process.env.DFB_STUB_IDLE, String(Date.now()))
    try {
      await page.waitForSelector('#attention-count:not([hidden])', { timeout: 20000 })
    } catch {
      fail(3, 'a finished OpenCode turn never reached the bell')
    }
    await openBell(page)
    const reply = (await page.locator('#attention-list .attention-go').first().textContent()) || ''
    const replySource = (await page.locator('#attention-list .attention-row-source').first().textContent()) || ''
    if (!reply.includes('OpenCode replied') || !reply.includes('Good morning! What can I help you with today?')) {
      fail(3, `the reply row is wrong: ${JSON.stringify(reply)}`)
    }
    if (!replySource.includes(workspaceName)) {
      fail(3, `the reply row does not name its workspace: ${JSON.stringify(replySource)}`)
    }
    await page.screenshot({ path: `${process.env.DFB_ROOT}/activity-reply.png` })
    await page.locator('#attention-list .attention-dismiss').first().click()
    await sleep(400)
    if (await page.locator('#attention-count').isVisible()) {
      fail(3, 'dismissing the reply row did not clear the bell')
    }

    console.log(JSON.stringify({ baselineQuestion: true, source: true, manualClear: true, noReplay: true, replyActivity: true }))
  } finally {
    await browser.close()
  }
}

main().catch((error) => fail(2, error.stack || error.message))
