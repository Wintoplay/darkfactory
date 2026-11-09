// @ts-check
'use strict'

/* DarkFactory browser client.
 *
 * The daemon owns the PTYs; this page is a client that can disappear. The
 * token lives in sessionStorage (this tab only), never in localStorage.
 */

/**
 * @typedef {object} Repo
 * @property {string} id
 * @property {string} name
 * @property {string} path
 * @property {boolean} git
 * @property {number} [position] Fleet order of the repo group; absent or 0
 *   means the group has never been dragged and sorts after the positioned ones.
 * @property {string} createdAt
 */

/**
 * @typedef {object} Worktree
 * @property {string} id
 * @property {string} repoId
 * @property {string} path
 * @property {string} branch
 * @property {string} baseRef
 * @property {boolean} [workspace]
 * @property {string} [containment]
 * @property {number} [position] Fleet order inside the repo; absent or 0 means
 *   the row has never been dragged and sorts after the positioned ones.
 * @property {string} createdAt
 */

/**
 * @typedef {object} Session
 * @property {string} id
 * @property {string} worktreeId
 * @property {string} kind
 * @property {string} [agent]
 * @property {string} [origin]
 * @property {string} command
 * @property {string[]} [args]
 * @property {string} title
 * @property {'running'|'exited'|'lost'} status
 * @property {number} cols
 * @property {number} rows
 * @property {number} [exitCode]
 * @property {number} [pid]
 * @property {number} [pgid]
 * @property {string} [incarnationId]
 * @property {string} [containment]
 * @property {string} [reason]
 * @property {string} [endedAt]
 * @property {boolean} [pinned] Kept at the top of its workspace's list.
 * @property {number} [position] Drag order inside the workspace; absent or 0
 *   means the row has never been dragged and sorts after the positioned ones.
 * @property {string} createdAt
 * @property {boolean} live
 */

/**
 * @typedef {object} OpenCodePart
 * @property {string} [type]
 * @property {string} [text]
 * @property {string} [name]
 * @property {{status?: string, input?: any, content?: Array<{type?: string, text?: string}>, error?: any}} [state]
 */

/**
 * @typedef {object} OpenCodeMessage
 * @property {string} id
 * @property {'user'|'assistant'|'idle'|'system'|'synthetic'} type
 * @property {string} [text]
 * @property {OpenCodePart[]} [content]
 * @property {{created?: number, streamed?: number, completed?: number}} [time]
 * @property {string} [outcome]
 * @property {any} [error]
 */

/**
 * @typedef {object} OpenCodeSession
 * @property {string} id
 * @property {string} [title]
 * @property {string} [agent]
 * @property {{id?: string, providerID?: string}} [model]
 * @property {{created: number, updated: number, idle?: number, viewed?: number}} time
 * @property {string} [outcome]
 * @property {{directory?: string}} [location]
 */

/**
 * @typedef {object} OpenCodePermission
 * @property {string} id
 * @property {string} sessionID
 * @property {string} action
 * @property {string[]} resources
 * @property {string[]} [save]
 * @property {string} [message]
 * @property {{type?: string}} [source]
 */

/**
 * @typedef {object} Device
 * @property {string} id
 * @property {string} label
 * @property {'full'|'read'} scope
 * @property {boolean} [terminal]
 * @property {string} createdAt
 * @property {string} lastSeenAt
 * @property {string} expiresAt
 * @property {boolean} current
 */

/**
 * @typedef {object} NativeTerminal
 * @property {boolean} available
 * @property {string} [version]
 * @property {string} [reason]
 */

/**
 * @typedef {object} AgentPreset
 * @property {string} command
 * @property {string[]} [args]
 * @property {string[]} [envKeys]
 */

/**
 * @typedef {object} PlanStep
 * @property {'command'|'exec'|'pty'} kind
 * @property {string} [name]
 * @property {string[]} [command]
 * @property {string} [agent]
 * @property {string[]} [args]
 * @property {string} [title]
 */

/**
 * @typedef {object} Run
 * @property {string} id
 * @property {string} worktreeId
 * @property {string} goal
 * @property {string} planHash
 * @property {'draft'|'running'|'waiting'|'blocked'|'succeeded'|'failed'|'cancelled'} state
 * @property {string} [outcome]
 * @property {string} [error]
 * @property {string} [startedAt]
 * @property {string} [endedAt]
 * @property {string} createdAt
 */

/**
 * @typedef {object} Step
 * @property {string} id
 * @property {string} runId
 * @property {number} ordinal
 * @property {number} visit
 * @property {'command'|'exec'|'pty'} kind
 * @property {string} [name]
 * @property {PlanStep} spec
 * @property {string} planHash
 * @property {'pending'|'running'|'waiting'|'blocked'|'succeeded'|'failed'|'skipped'} state
 * @property {number} attempt
 * @property {number} [exitCode]
 * @property {string} [output]
 * @property {string} [error]
 * @property {string} [sessionId]
 * @property {string} [startedAt]
 * @property {string} [endedAt]
 */

/**
 * @typedef {object} RunDetail
 * @property {Run} run
 * @property {{hash: string, steps: PlanStep[], createdAt: string}} plan
 * @property {Step[]} steps
 */

/**
 * @typedef {object} State
 * @property {Repo[]} repos
 * @property {Worktree[]} worktrees
 * @property {Session[]} sessions
 * @property {Run[]} runs
 * @property {Record<string, AgentPreset>} agents
 * @property {string} [opencodeURL]
 * @property {boolean} [opencode]
 * @property {'full'|'read'} [scope]
 * @property {boolean} [terminal]
 * @property {string} [build] daemon version + commit, printed in the footer
 * @property {string} [ui] shell version the daemon embeds, for stale-page reload
 * @property {NativeTerminal} [nativeTerminal] host-local Ghostty capability
 * @property {Device} [device]
 */

/**
 * @typedef {object} DirEntry
 * @property {string} name
 * @property {string} path
 * @property {boolean} git
 */

/**
 * @typedef {object} DirsResponse
 * @property {string} path
 * @property {string} [parent]
 * @property {string} home
 * @property {boolean} git
 * @property {DirEntry[]} entries
 * @property {boolean} truncated
 */

/** @typedef {{kind: 'session'|'worktree'|'runs'|'opencode', id?: string}} ViewSelection */

// The id comes from our own markup; a missing node is a bug, not a state to
// handle. The DOM type is narrowed at the call site by the property used.
/**
 * @param {string} id
 * @returns {HTMLElement}
 */
const $ = (id) => {
  const node = document.getElementById(id)
  if (!node) throw new Error(`missing #${id}`)
  return node
}

/** @param {string} id @returns {HTMLInputElement} */
const inputEl = (id) => /** @type {HTMLInputElement} */ ($(id))
/** @param {string} id @returns {HTMLSelectElement} */
const selectEl = (id) => /** @type {HTMLSelectElement} */ ($(id))
/** @param {string} id @returns {HTMLButtonElement} */
const buttonEl = (id) => /** @type {HTMLButtonElement} */ ($(id))
/** @param {string} id @returns {HTMLFormElement} */
const formEl = (id) => /** @type {HTMLFormElement} */ ($(id))
/** @param {string} id @param {string} selector @returns {HTMLInputElement|null} */
const inputIn = (id, selector) => /** @type {HTMLInputElement|null} */ (formEl(id).querySelector(selector))
/** @param {string} id @returns {HTMLDialogElement} */
const dialogEl = (id) => /** @type {HTMLDialogElement} */ ($(id))
/** @param {string} id @returns {HTMLDetailsElement} */
const detailsEl = (id) => /** @type {HTMLDetailsElement} */ ($(id))

/** @param {Event} event @returns {Element} */
function eventElement(event) {
  const target = event.target
  if (!(target instanceof Element)) throw new Error('event target is not an element')
  return target
}

/** @param {Event} event @returns {HTMLFormElement} */
function eventForm(event) {
  const form = event.currentTarget
  if (!(form instanceof HTMLFormElement)) throw new Error('event has no form')
  return form
}

const TOKEN_KEY = 'darkfactory-token'
// A device key is a non-extractable ECDSA P-256 private key in IndexedDB. It
// is not a bearer token: the server stores only the public half, and every
// launch signs a fresh one-use challenge. Cookie auth stays for browsers.
const DEVICE_DB = 'darkfactory-device-keys'
const DEVICE_STORE = 'keys'
const DEVICE_RECORD = 'device'
const TERM_FONT_KEY = 'darkfactory-term-font'
const TERM_FONT_DEFAULT = 12.5
const TERM_FONT_MIN = 10
const TERM_FONT_MAX = 20
const POLL_MS = 3000
const ATTENTION_POLL_MS = 3000
// One OpenCode chat can carry megabytes of tool output. Fetch a page the panel
// can render without blocking input; "Load older" pages the rest. Refreshing
// the same page more often than this re-downloads it during streaming.
const OPENCODE_MESSAGE_PAGE = 40
const OPENCODE_MESSAGE_MIN_INTERVAL = 1500
// Bump this when the browser client changes. It is printed in the footer and
// the debug panel, so a stale cached app.js is visible instead of mysterious.
const UI_VERSION = '2026-11-09.01'
const QUERY = new URLSearchParams(location.search)
const DEBUG = QUERY.get('debug') === '1'
// The panel floats over the lower terminal pane, which hides what a screenshot
// test is looking at. `?debug=1&panel=0` keeps the log and its POSTs without
// covering the pane; closing the panel by hand does the same thing.
const DEBUG_PANEL = DEBUG && QUERY.get('panel') !== '0'
// Chat mode is a layout choice, not a second chat client: it hides the fleet
// chrome and gives the OpenCode panel the viewport. `?chat=1` / `?chat=0` force
// it for one tab so the panel layout and the chat layout can be compared side by
// side; with no parameter the tab remembers what it last chose.
const CHAT_QUERY = QUERY.get('chat')
const CHAT_MODE_KEY = 'df-chat-mode'

// xterm.js creates <style> nodes at runtime. The daemon serves this page with
// a per-response CSP nonce; stamp it onto those nodes so style-src stays
// nonce-gated. xterm's truecolor style attributes are allowed separately by
// style-src-attr; inline scripts remain blocked.
const CSP_NONCE = document.querySelector('meta[name="csp-nonce"]')?.getAttribute('content') || ''
if (CSP_NONCE) {
  const nativeCreateElement = document.createElement.bind(document)
  Object.defineProperty(document, 'createElement', {
    configurable: true,
    value: function (/** @type {string} */ tag, /** @type {any} */ options) {
      const node = /** @type {HTMLElement} */ (nativeCreateElement(tag, options))
      if (tag === 'style' || tag === 'link') node.setAttribute('nonce', CSP_NONCE)
      return node
    }
  })
}

/** @type {string} */
let token = readToken()
// Cookie auth has no bearer token in the page. Keep authentication separate
// from the token so refresh and polling still work after `darkfactory open`.
let authenticated = false
let deviceAuthBusy = false
// Disconnect is a deliberate end to this tab's session. It must not be undone
// by the automatic device-key retry until the next launch or a manual connect.
let deviceAuthPaused = false
/** @type {Promise<void>|null} */
let deviceKeyRegistration = null
/** @type {'connected'|'connecting'|'offline'} */
let connState = 'offline'
/** @type {State} */
let state = { repos: [], worktrees: [], sessions: [], runs: [], agents: {}, opencodeURL: '', opencode: false, scope: 'full', terminal: true, build: '', ui: '' }
/** @type {ViewSelection|null} */
let selection = null
/** @type {Session|null} */
let currentSession = null
// One key per launch intent. A retry after a lost response replays the session
// the daemon already started instead of starting a twin PTY.
let sessionRequestKey = ''
const restarting = new Set()
/** @type {string} */
let sessionTargetWorktreeId = ''
/** @type {RunDetail|null} */
let runDetail = null
let currentRunId = ''
let runDetailLoading = false
let runDetailSeq = 0

/** @typedef {object} AttentionEvent
 * @property {number} id
 * @property {string} kind
 * @property {string} title
 * @property {string} [body]
 * @property {string} [sessionId]
 * @property {string} [worktreeId]
 * @property {string} [sessionTitle]
 * @property {string} [openCodeSessionId]
 * @property {string} [at]
 */
/** @type {AttentionEvent[]} */
let attentionEvents = []
let attentionCursor = 0
let attentionTimer = 0
let attentionBusy = false
let attentionOpen = false

let openCodeSessions = []
let openCodeSessionId = ''
// Chat is a view of one worktree, never a peer of the fleet. These remember
// which worktree the panel is scoped to so a stray refresh cannot widen it.
let openCodeScopeWorktreeId = ''
let openCodeScopeDirectory = ''
let openCodeMessages = []
let openCodeMessagesFetchedAt = 0
let openCodeSessionSeq = 0
let openCodeMessageSeq = 0
let openCodePollTimer = 0
let openCodePollTick = 0
let openCodeSending = false
let openCodeInterrupting = false
let openCodeStick = true
// Set while a programmatic jump to the bottom is landing, so the scroll
// listener does not recompute the stick flag from a partial position.
let openCodeJumping = false
let openCodeListSignature = ''
// A message key is empty for a chat with no messages, so clearing the signature
// to '' and then rendering an empty chat used to match the previous render and
// skip the repaint: the previous chat's transcript stayed on screen. Reset to a
// value no real key can take instead.
const OPENCODE_MESSAGE_STALE = 'stale'
let openCodeMessageSignature = ''
let openCodeSearch = ''
let openCodeSearchTimer = 0
let openCodeOlderCursor = ''
let openCodeOlderLoading = false
let openCodeLoadedOlder = false
let openCodePermissions = []
let openCodePermissionSeq = 0
let openCodePermissionReplying = ''
let openCodeEventAbort = null
let openCodeEventTimer = 0
let openCodeEventRetry = 0
/** @type {{name: string, description: string}[]} */
let openCodeCommands = []
let openCodeCommandLocation = ''
let openCodeCommandsLoaded = false
let openCodeCommandsLoading = false
let openCodeCommandIndex = 0
/** @type {boolean} */
let chatMode = CHAT_QUERY === '1' ? true : CHAT_QUERY === '0' ? false : readChatMode()
// What the DOM is showing right now, which is not the same thing as the
// preference: leaving the OpenCode view clears the layout and keeps the choice
// for the next visit.
let chatLayout = false
/** @type {Record<string, string>} */
const openCodeBoxes = {}
/** @type {Set<string>} */
const expandedWorktrees = new Set()
/** @type {{kind: 'session'|'worktree', id: string}|null} */
let contextTarget = null
/** @type {AbortController|null} */
let streamAbort = null
let streaming = false
let detached = false
let nativePaneActive = false
let nativePaneSession = ''
let nativePaneHidden = false
let nativeBoundsTimer = 0
let nativeDisabled = false
let tuiSession = false
let resizeReady = false
let pollTimer = 0
let refreshSeq = 0
let connectSeq = 0
let connectBusy = false
let pairBusy = false
/** @type {((ok: boolean) => void)|null} */
let confirmResolve = null
let toastTimer = 0
/** @type {Terminal|null} */
let terminal = null
/** @type {FitAddon|null} */
let fitAddon = null
// The grid size the last successful fit applied. Every fit repaints the visible
// rows, and the fit triggers overlap (ResizeObserver, window resize, visual
// viewport, settle poll), so a fit that proposes the same grid is skipped.
let lastFitKey = ''
let webglLoadStarted = false
let webglAttached = false
let termFontSize = TERM_FONT_DEFAULT
let terminalSessionId = ''
let terminalGeneration = 0
let terminalInputError = false
// Input is coalesced within the current event-loop turn. At most one request
// is in flight; the next batch waits for it to preserve input order.
let pendingInput = ''
let inputFlushScheduled = false
let inputInFlight = false
// A throttled or briefly unreachable daemon must not eat keystrokes. The batch
// is put back in front of anything typed since, and retried a few times with a
// growing delay.
let inputRetryTimer = 0
let inputRetryCount = 0
// PTY output is coalesced per animation frame for the same reason. The DOM
// renderer is the slow path on an iPad, so do not ask it to parse every tiny
// network chunk separately. A small chunk (interactive echo) skips the frame.
let termWriteQueue = ''
let termWriteFrame = 0
const TERM_WRITE_NOW = 8 * 1024
const TERM_QUEUE_HIGH = 256 * 1024
/** @type {Array<() => void>} */
/** @type {Array<() => void>} */
let termDrainWaiters = []
let resizeSendTimer = 0
/** @type {{cols: number, rows: number}|null} */
let pendingResize = null
/** @type {DirsResponse|null} */
let browserState = null
let browserSeq = 0
/** @type {string[]} */
let debugQueue = []
let debugFlushTimer = 0

// 1200 was also the Wails default width, so a desktop window opened in the
// phone layout: no sidebar, no view head, no Chat button. 900 keeps phones and
// small tablets; any-pointer: coarse still covers touch devices.
const MOBILE_QUERY = '(max-width: 900px), (any-pointer: coarse)'
const isMobile = () => window.matchMedia(MOBILE_QUERY).matches
const readOnly = () => state.scope === 'read'
// A read-only device can see state, runs and chat transcripts but never a PTY
// byte or a chat mutation. The daemon refuses the routes too; this only keeps
// the UI from offering a control that can only fail.
const hasTerminal = () => state.terminal !== false

// iPad Safari keeps the layout viewport at full height when the keyboard opens;
// only visualViewport shrinks. Drive the shell from that height so the
// composer/terminal bottom edge stays above the keyboard.
let lastViewportLog = ''
function logViewport(label) {
  if (!DEBUG) return
  const viewport = window.visualViewport
  const line = `${label} inner=${window.innerWidth}x${window.innerHeight} vv=${viewport ? `${Math.round(viewport.width)}x${Math.round(viewport.height)}` : '-'} top=${viewport ? Math.round(viewport.offsetTop) : '-'} scale=${viewport ? viewport.scale.toFixed(2) : '-'} ${window.matchMedia('(orientation: landscape)').matches ? 'landscape' : 'portrait'}`
  if (line === lastViewportLog) return
  lastViewportLog = line
  debugLog('viewport', line)
}

// visualViewport.scroll fires at pan speed, and every write here relayouts the
// fixed shell with the terminal canvas inside it. Coalesce to one write per
// frame and skip values that did not change, or a pan becomes layout thrash.
let appHeightFrame = 0
let appliedHeight = -1
let appliedTop = -1
let keyboardOpen = false
function applyAppHeight() {
  appHeightFrame = 0
  const viewport = window.visualViewport
  const height = Math.round(viewport ? viewport.height : window.innerHeight)
  const top = viewport ? Math.round(viewport.offsetTop) : 0
  if (height <= 0) return
  const style = document.documentElement.style
  if (height !== appliedHeight) {
    appliedHeight = height
    style.setProperty('--app-height', `${height}px`)
  }
  if (top !== appliedTop) {
    appliedTop = top
    style.setProperty('--app-top', `${top}px`)
  }
  // Without a keyboard the shell is pinned to the layout viewport, so a phone
  // whose visual viewport reports a couple of pixels less does not leave a
  // dead strip under the bottom nav.
  const raised = height < window.innerHeight - 120
  if (raised !== keyboardOpen) {
    keyboardOpen = raised
    const root = document.documentElement
    if (raised) root.dataset.keyboard = '1'
    else delete root.dataset.keyboard
  }
  logViewport('sync')
}

function syncAppHeight() {
  if (appHeightFrame) return
  appHeightFrame = window.requestAnimationFrame(applyAppHeight)
}
syncAppHeight()
window.addEventListener('resize', () => {
  syncAppHeight()
  settleTerminalViewport()
})
window.addEventListener('orientationchange', () => {
  syncAppHeight()
  settleTerminalViewport()
})
if (window.visualViewport) {
  window.visualViewport.addEventListener('resize', () => {
    syncAppHeight()
    settleTerminalViewport()
    reportOpenCodeBoxes()
  })
  window.visualViewport.addEventListener('scroll', syncAppHeight)
}

// Both iOS and Android discard the drawing buffer when the tab or app goes to
// the background. On return the canvas can be blank until something writes to
// it, so force a real fit (a fresh GL context starts unsized) and repaint.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return
  applyAppHeight()
  lastFitKey = ''
  settleTerminalViewport()
  refreshTerminal()
})

// The keyboard animation finishes after focus and iOS reports the final visual
// viewport late (landscape is worst), so a fit at a fixed delay lands on the
// wrong row count and then has to repaint the whole grid again. Poll the
// reported size instead and fit once it has held still for two frames, with a
// hard cap so a viewport that keeps moving still gets one fit.
let settleFrame = 0
let settleHeight = -1
let settleTop = -1
let settleStable = 0
let settleDeadline = 0
function pollViewportSettle() {
  settleFrame = 0
  if (selection?.kind !== 'session' || !terminal) {
    settleDeadline = 0
    return
  }
  applyAppHeight()
  const viewport = window.visualViewport
  const height = Math.round(viewport ? viewport.height : window.innerHeight)
  const top = viewport ? Math.round(viewport.offsetTop) : 0
  const now = performance.now()
  if (!settleDeadline) settleDeadline = now + 1000
  if (height === settleHeight && top === settleTop) {
    settleStable++
  } else {
    settleStable = 0
    settleHeight = height
    settleTop = top
  }
  if (settleStable < 2 && now < settleDeadline) {
    settleFrame = window.requestAnimationFrame(pollViewportSettle)
    return
  }
  settleDeadline = 0
  if (fitTerminal()) {
    const buffer = terminal.buffer.active
    if (buffer.viewportY >= buffer.baseY) terminal.scrollToBottom()
  }
}

function settleTerminalViewport() {
  // Only start the cap on a fresh settle. A resize that fires while the poll is
  // already running must not push the deadline out, or a viewport that never
  // stops moving would never fit.
  if (!settleDeadline) settleDeadline = performance.now() + 1000
  settleStable = 0
  settleHeight = -1
  settleTop = -1
  applyAppHeight()
  if (!settleFrame) settleFrame = window.requestAnimationFrame(pollViewportSettle)
}
document.addEventListener('focusin', (event) => {
  const target = event.target
  if (!(target instanceof HTMLElement)) return
  if (!target.matches('input, textarea, select, [contenteditable="true"]')) return
  syncAppHeight()
  settleTerminalViewport()
  if (DEBUG) debugLog('viewport', `focusin ${target.className || target.tagName}`)
})
document.addEventListener('focusout', () => {
  settleTerminalViewport()
  if (DEBUG) debugLog('viewport', 'focusout')
})

// Stay tolerant of an older daemon that encoded empty lists as null.
/** @param {any} next @returns {State} */
function normalizeState(next) {
  /** @type {'full'|'read'} */
  const scope = next?.scope === 'read' ? 'read' : 'full'
  return {
    repos: Array.isArray(next?.repos) ? next.repos : [],
    worktrees: Array.isArray(next?.worktrees) ? next.worktrees : [],
    sessions: Array.isArray(next?.sessions) ? next.sessions : [],
    runs: Array.isArray(next?.runs) ? next.runs : [],
    agents: next?.agents && typeof next.agents === 'object' ? next.agents : {},
    opencodeURL: typeof next?.opencodeURL === 'string' ? next.opencodeURL : '',
    opencode: next?.opencode === true,
    scope,
    // Older daemons do not send the flag and had no gate, so absence means
    // "allowed"; only an explicit false takes the terminal away.
    terminal: next?.terminal !== false,
    build: typeof next?.build === 'string' ? next.build : '',
    // These two are read by the stale-shell reload and the Ghostty gate. The
    // whitelist must carry them or the daemon's answers never reach the page.
    ui: typeof next?.ui === 'string' ? next.ui : '',
    nativeTerminal: next?.nativeTerminal && typeof next.nativeTerminal === 'object' ? next.nativeTerminal : undefined,
    device: next?.device && typeof next.device === 'object' ? next.device : undefined
  }
}

/* ---------- small helpers ---------- */

function el(tag, className, text) {
  const node = document.createElement(tag)
  if (className) node.className = className
  if (text !== undefined) node.textContent = text
  return node
}

function dot(kind) {
  return el('i', `dot ${kind || 'idle'}`)
}

/** State glyph. A 12px box keeps row/tab geometry stable. */
function activityIcon(kind, label) {
  const node = el('span', `agent-state ${kind}`)
  node.setAttribute('role', 'img')
  node.setAttribute('aria-label', label)
  node.title = label
  if (kind === 'done') node.textContent = '✓'
  else if (kind === 'attention' || kind === 'lost') node.textContent = '!'
  else if (kind === 'unread') node.append(bellGlyph())
  return node
}

function bellGlyph() {
  const ns = 'http://www.w3.org/2000/svg'
  const svg = document.createElementNS(ns, 'svg')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '11')
  svg.setAttribute('height', '11')
  svg.setAttribute('aria-hidden', 'true')
  const path = document.createElementNS(ns, 'path')
  path.setAttribute(
    'd',
    'M8 1.6a3.6 3.6 0 0 0-3.6 3.6v2.9L3 10.3h10l-1.4-2.2V5.2A3.6 3.6 0 0 0 8 1.6Zm0 12.1a1.9 1.9 0 0 0 1.9-1.6H6.1A1.9 1.9 0 0 0 8 13.7Z'
  )
  path.setAttribute('fill', 'currentColor')
  svg.append(path)
  return svg
}

/** @param {Session} sess */
function sessionActivity(sess) {
  if (attentionEvents.some((event) => event.sessionId === sess.id)) {
    return activityIcon('unread', 'Unread agent activity')
  }
  // A live PTY is not proof that an arbitrary TUI is working: a blank Codex or
  // an idle OpenCode prompt has a live PTY too. Use a static running dot and
  // reserve motion for data we actually control.
  if (sess.live) return activityIcon('live', 'Running')
  if (sess.status === 'lost') return activityIcon('lost', 'Session lost')
  if (sess.status === 'exited') return activityIcon('done', 'Finished')
  return activityIcon('idle', sess.status || 'Idle')
}

function renderAttention() {
  const count = attentionEvents.length
  const text = count > 99 ? '99+' : String(count)
  const label = count === 0
    ? 'Agent activity: nothing unread'
    : `${count} unread agent ${count === 1 ? 'event' : 'events'}`
  const button = /** @type {HTMLButtonElement} */ ($('btn-attention'))
  button.title = count === 0 ? 'Activity' : label
  button.setAttribute('aria-label', label)
  button.disabled = false
  // The bell is a list, not a one-shot button: it opens even when nothing is
  // unread, so the owner can check what happened instead of clicking a dead
  // icon. Only the count and the colour change with activity.
  button.classList.toggle('has-unread', count > 0)
  button.setAttribute('aria-expanded', String(attentionOpen))
  const countNode = $('attention-count')
  countNode.textContent = text
  countNode.hidden = count === 0
  const badge = $('nav-attention')
  badge.hidden = count === 0
  badge.textContent = text
  $('attention-clear').hidden = count === 0
  if (!count) closeAttentionPanel()
  else if (attentionOpen) renderAttentionList()
}

/** @param {AttentionEvent} event */
function attentionGlyph(event) {
  if (event.kind === 'session.lost') return 'lost'
  // A finished PTY session and a finished OpenCode turn are both done, not
  // something waiting on the owner.
  if (event.kind === 'session.exited' || event.kind === 'opencode.replied') return 'done'
  return 'attention'
}

// The source line is the point of the panel: an OpenCode permission has no PTY
// session, so the chat title plus its workspace is the only way to know which
// agent is blocked.
/** @param {AttentionEvent} event */
function attentionSessionName(event) {
  if (event.sessionTitle) return event.sessionTitle
  const sess = event.sessionId ? sessionFor(event.sessionId) : null
  if (sess) return sess.title || agentLabel(sess)
  const chat = event.openCodeSessionId
    ? openCodeSessions.find((row) => row.id === event.openCodeSessionId)
    : null
  if (chat?.title) return chat.title
  if (event.openCodeSessionId) return `chat ${shortId(event.openCodeSessionId)}`
  return ''
}

function attentionSource(event) {
  const parts = []
  const name = attentionSessionName(event)
  if (name) parts.push(name)
  const wt = event.worktreeId ? worktreeFor(event.worktreeId) : null
  if (wt) parts.push(wtName(wt))
  if (event.at) parts.push(timeShort(event.at))
  return parts.join(' · ')
}

function renderAttentionList() {
  const list = $('attention-list')
  list.replaceChildren()
  const events = attentionEvents.slice().reverse()
  if (!events.length) {
    list.append(el('li', 'attention-empty', 'Nothing unread. Finished sessions, agent replies, permission requests and questions appear here.'))
    return
  }
  for (const event of events) {
    const row = el('li', 'attention-row')
    const go = el('button', 'attention-go')
    go.type = 'button'
    go.dataset.attentionGo = String(event.id)
    const head = el('span', 'attention-row-title')
    head.append(activityIcon(attentionGlyph(event), event.kind || 'activity'))
    head.append(el('span', '', event.title))
    go.append(head)
    // The source goes above the body: a long answer must not push the session
    // name out of the visible row, which is what made the list look anonymous.
    go.append(el('span', 'attention-row-source', attentionSource(event)))
    if (event.body) go.append(el('span', 'attention-row-body', event.body))
    row.append(go)

    const dismiss = el('button', 'attention-dismiss', '×')
    dismiss.type = 'button'
    dismiss.dataset.attentionDismiss = String(event.id)
    dismiss.title = 'Dismiss this event'
    dismiss.setAttribute('aria-label', `Dismiss ${event.title}`)
    row.append(dismiss)
    list.append(row)
  }
}

function openAttentionPanel() {
  const pop = $('attention-pop')
  const button = $('btn-attention')
  renderAttentionList()
  pop.hidden = false
  // Fixed, anchored under the bell and clamped to the viewport: the topbar
  // wraps on a phone, so an absolutely positioned panel would hang off the
  // left edge where the bell no longer sits.
  const anchor = button.getBoundingClientRect()
  const box = pop.getBoundingClientRect()
  const x = Math.max(8, Math.min(anchor.right - box.width, window.innerWidth - box.width - 8))
  const y = Math.min(anchor.bottom + 8, Math.max(8, window.innerHeight - box.height - 8))
  pop.style.left = `${Math.round(x)}px`
  pop.style.top = `${Math.round(y)}px`
  attentionOpen = true
  button.setAttribute('aria-expanded', 'true')
  // The in-app pane is a GTK overlay child and paints above the WebView, so it
  // would cover the lower half of this panel. Hide it while the list is open;
  // the PTY stays attached and the pane returns on close.
  syncNativePaneVisibility()
  const first = /** @type {HTMLButtonElement|null} */ (pop.querySelector('#attention-list button'))
  first?.focus()
  // Read the ring before the owner looks at it: a poll only carries events after
  // the cursor, so a row that already arrived would never learn the workspace
  // the daemon linked afterwards, and a window that was hidden has catching up
  // to do.
  void refreshAttention()
}

function closeAttentionPanel() {
  const pop = $('attention-pop')
  if (!attentionOpen && pop.hidden) return
  attentionOpen = false
  pop.hidden = true
  $('btn-attention').setAttribute('aria-expanded', 'false')
  syncNativePaneVisibility()
}

function toggleAttentionPanel() {
  if (attentionOpen) closeAttentionPanel()
  else openAttentionPanel()
}

/** @param {number} id */
function dismissAttention(id) {
  const kept = attentionEvents.filter((event) => event.id !== id)
  if (kept.length === attentionEvents.length) return
  attentionEvents = kept
  renderAttention()
  renderTree()
  if (currentSession) renderSessionTabs()
  // Clear it on the daemon too: the ring is what a reload reads, so a local
  // removal alone would bring the row back.
  void api('/api/v1/notifications/dismiss', { method: 'POST', body: { ids: [id] } })
    .catch((error) => { if (error.message !== 'unauthorized') toast(error.message, true) })
}

function clearAttention() {
  // Do not rewind the cursor: the cleared rows are still in the daemon's ring,
  // and a rewind would bring them straight back on the next poll. The daemon
  // clears its ring too, so a reload starts empty as well.
  attentionEvents = []
  closeAttentionPanel()
  renderAttention()
  void api('/api/v1/notifications/clear', { method: 'POST', body: {} })
    .catch((error) => { if (error.message !== 'unauthorized') toast(error.message, true) })
}

const ALERT_SWITCHES = {
  'alert-blocked': 'blockedRuns',
  'alert-prompts': 'prompts',
  'alert-finished': 'finishedSessions',
  'alert-replies': 'replies',
  'alert-children': 'childSessions'
}

/** @param {any} settings */
function fillAlertSettings(settings) {
  const source = settings || {}
  for (const [id, key] of Object.entries(ALERT_SWITCHES)) {
    const box = /** @type {HTMLInputElement|null} */ (document.getElementById(id))
    if (box) box.checked = Boolean(source[key])
  }
}

function readAlertSettings() {
  /** @type {Record<string, boolean>} */
  const out = {}
  for (const [id, key] of Object.entries(ALERT_SWITCHES)) {
    const box = /** @type {HTMLInputElement|null} */ (document.getElementById(id))
    out[key] = Boolean(box?.checked)
  }
  return out
}

async function openAlertSettings() {
  closeAttentionPanel()
  $('alerts-note').textContent = ''
  fillAlertSettings({
    blockedRuns: true,
    prompts: true,
    finishedSessions: true,
    replies: false,
    childSessions: false
  })
  openDialog('dlg-alerts')
  try {
    fillAlertSettings(await api('/api/v1/notifications/settings'))
  } catch (error) {
    $('alerts-note').textContent = 'Could not read alert settings; showing defaults.'
    debugLog('error', `alert settings: ${error.message}`)
  }
}

async function saveAlertSettings(event) {
  event.preventDefault()
  const button = /** @type {HTMLButtonElement} */ ($('btn-alerts-save'))
  button.disabled = true
  try {
    const saved = await api('/api/v1/notifications/settings', {
      method: 'POST',
      body: readAlertSettings()
    })
    fillAlertSettings(saved)
    closeDialog(dialogEl('dlg-alerts'))
    toast('Alert settings saved.')
  } catch (error) {
    if (error.message !== 'unauthorized') $('alerts-note').textContent = error.message
  } finally {
    button.disabled = false
  }
}

// A chat is scoped to a worktree, not to a PTY, so an OpenCode row follows to
// that worktree's terminal: the live `opencode` session first, then any live
// session, then the newest one. Null means the worktree has no session yet and
// the caller should show its session page instead.
/** @param {string} worktreeId @returns {Session|null} */
function chatSessionTarget(worktreeId) {
  const sessions = worktreeId
    ? state.sessions.filter((sess) => sess.worktreeId === worktreeId)
    : []
  if (!sessions.length) return null
  const newest = sessions.slice().sort((a, b) =>
    String(b.createdAt || '').localeCompare(String(a.createdAt || '')))
  const live = newest.filter((sess) => sess.live)
  return live.find((sess) => (sess.agent || '') === 'opencode')
    || live[0]
    || newest[0]
    || null
}

// A row that still has a target opens it and stays unread until the owner
// dismisses it: clicking the wrong row must not lose the event. A row whose
// session or worktree is gone says so instead of navigating nowhere. The bell
// follows the work, not the answer box: an OpenCode row opens the workspace's
// terminal session, never the chat panel.
/** @param {AttentionEvent} event */
function followAttention(event) {
  closeAttentionPanel()
  const sess = event.sessionId ? sessionFor(event.sessionId) : null
  if (sess) {
    void openSession(sess)
    return
  }
  if (event.openCodeSessionId) {
    const wt = event.worktreeId ? worktreeFor(event.worktreeId) : null
    const target = chatSessionTarget(event.worktreeId || '')
    if (target) {
      void openSession(target)
      return
    }
    if (wt) {
      selectWorktree(wt.id, false)
      return
    }
    toast("That chat's workspace is gone; open the worktree from the sidebar.", true)
    return
  }
  const wt = event.worktreeId ? worktreeFor(event.worktreeId) : null
  if (wt) {
    selectWorktree(wt.id, false)
    return
  }
  toast(`${event.title}${event.body ? ` — ${event.body}` : ''}`)
}

/** @param {any} raw @returns {AttentionEvent} */
function attentionEvent(raw) {
  return {
    id: Number(raw?.id) || 0,
    kind: String(raw?.kind || ''),
    title: String(raw?.title || 'DarkFactory'),
    body: String(raw?.body || ''),
    sessionId: raw?.sessionId ? String(raw.sessionId) : '',
    worktreeId: raw?.worktreeId ? String(raw.worktreeId) : '',
    sessionTitle: raw?.sessionTitle ? String(raw.sessionTitle) : '',
    openCodeSessionId: raw?.openCodeSessionId ? String(raw.openCodeSessionId) : '',
    at: raw?.at ? String(raw.at) : ''
  }
}

/** @param {any[]} incoming @returns {boolean} */
function mergeAttention(incoming) {
  let added = false
  let updated = false
  for (const raw of incoming) {
    const event = attentionEvent(raw)
    if (!event.id) continue
    const known = attentionEvents.find((row) => row.id === event.id)
    if (known) {
      // A prompt learned its workspace after the row arrived: fill the link in
      // instead of dropping the update as a duplicate.
      if (!known.worktreeId && event.worktreeId) {
        known.worktreeId = event.worktreeId
        updated = true
      }
      if (!known.sessionTitle && event.sessionTitle) {
        known.sessionTitle = event.sessionTitle
        updated = true
      }
      if (event.body && event.body !== known.body) {
        known.body = event.body
        updated = true
      }
      continue
    }
    // Watching the session is already reading it. The Fleet or Runs view is
    // not, even when that session is still the selected one, so those events
    // must still raise a bell.
    const watching = selection?.kind === 'session' &&
      currentSession?.id === event.sessionId &&
      document.visibilityState === 'visible'
    if (event.sessionId && watching) continue
    attentionEvents.push(event)
    added = true
  }
  attentionEvents.sort((a, b) => a.id - b.id)
  if (attentionEvents.length > 50) attentionEvents = attentionEvents.slice(-50)
  if (added || updated) {
    renderAttention()
    renderTree()
    if (currentSession) renderSessionTabs()
  }
  return added
}

// A cursor poll never revisits a row it already delivered, so the open prompt's
// later links and anything missed while the window was hidden come from a read
// of the whole ring.
async function refreshAttention() {
  if (attentionBusy || !authenticated) return false
  attentionBusy = true
  try {
    const page = await api('/api/v1/notifications?after=0')
    const next = Number(page?.next) || 0
    if (next > attentionCursor) attentionCursor = next
    return mergeAttention(Array.isArray(page?.events) ? page.events : [])
  } catch (error) {
    debugLog('error', `notifications refresh: ${error.message}`)
    return false
  } finally {
    attentionBusy = false
  }
}

async function pollNotifications() {
  if (attentionBusy || !authenticated) return
  attentionBusy = true
  try {
    const page = await api(`/api/v1/notifications?after=${attentionCursor}`)
    attentionCursor = Number(page?.next) || 0
    mergeAttention(Array.isArray(page?.events) ? page.events : [])
  } catch (error) {
    debugLog('error', `notifications: ${error.message}`)
  } finally {
    attentionBusy = false
  }
}

function pill(text, kind, dotKind) {
  const node = el('span', `pill${kind ? ' ' + kind : ''}`)
  if (dotKind) node.append(dot(dotKind))
  node.append(document.createTextNode(text))
  return node
}

/**
 * Append to the debug panel and the console. Enable with `?debug=1`.
 * Never pass tokens, request bodies, or file contents here.
 * @param {string} area
 * @param {string} message
 */
function debugLog(area, message) {
  if (!DEBUG) return
  const stamp = new Date().toISOString().slice(11, 23)
  const line = `${stamp} ${area.padEnd(6)} ${message}`
  console.debug(`[darkfactory] ${line}`)
  const list = document.getElementById('debug-list')
  if (!list) return
  const item = el('li', `debug-line ${area}`)
  item.textContent = line
  list.append(item)
  while (list.childElementCount > 300) list.firstElementChild?.remove()
  list.scrollTop = list.scrollHeight
  queueClientDebug(line)
}

/** @param {string} line */
function queueClientDebug(line) {
  if (!DEBUG) return
  debugQueue.push(line)
  if (debugFlushTimer) return
  debugFlushTimer = window.setTimeout(() => { void flushClientDebug() }, 500)
}

// Best effort: the server mirrors this to its request log (and to debug.log
// with --debug). Raw fetch, not api(), so logging never logs itself.
async function flushClientDebug() {
  debugFlushTimer = 0
  if (!debugQueue.length) return
  const lines = debugQueue.splice(0, 100)
  /** @type {Record<string, string>} */
  const headers = { 'Content-Type': 'text/plain', 'X-DF-Request': '1' }
  if (token) headers.Authorization = `Bearer ${token}`
  try {
    await fetch('/api/v1/debug/client', {
      method: 'POST',
      headers,
      body: lines.join('\n'),
      credentials: 'same-origin',
      keepalive: true
    })
  } catch { /* debug transport is best effort */ }
}

/** @returns {string} */
function debugText() {
  const list = document.getElementById('debug-list')
  if (!list) return ''
  return Array.from(list.children).map((child) => child.textContent || '').join('\n')
}

/** @param {string} tag */
function logBrowserLayout(tag) {
  if (!DEBUG) return
  const panel = $('repo-browser')
  const dialog = dialogEl('dlg-repo')
  const pr = panel.getBoundingClientRect()
  const dr = dialog.getBoundingClientRect()
  const at = document.elementFromPoint(pr.left + pr.width / 2, pr.top + pr.height / 2)
  const top = at
    ? at.id || (at instanceof HTMLElement && at.className ? at.className : '') || at.tagName
    : 'none'
  debugLog('browse', `layout ${tag} panel=${Math.round(pr.top)}..${Math.round(pr.bottom)} dialog=${Math.round(dr.top)}..${Math.round(dr.bottom)} scroll=${dialog.scrollTop} hidden=${panel.hidden} at=${top}`)
}

/** @returns {string} a short call-site for debug logs */
function caller() {
  const stack = new Error().stack || ''
  return stack.split('\n').slice(2, 4).map((line) => line.trim()).join(' | ')
}

/** @param {string} message */
function showClientError(message) {
  const banner = document.getElementById('client-error')
  const label = document.getElementById('client-error-text')
  if (!banner || !label) return
  label.textContent = `UI error: ${String(message || 'unknown').slice(0, 240)}`
  banner.hidden = false
}

// A Safari-only failure otherwise leaves a dead-looking page with no clue.
window.addEventListener('error', (event) => {
  if (!event.message || event.message === 'ResizeObserver loop limit exceeded') return
  debugLog('error', `${event.message} (${event.filename}:${event.lineno}:${event.colno})`)
  showClientError(event.message)
})
window.addEventListener('unhandledrejection', (event) => {
  const reason = event.reason instanceof Error ? event.reason.message : String(event.reason)
  debugLog('error', `unhandled rejection: ${reason}`)
  showClientError(reason)
})

function initDebug() {
  if (!DEBUG) return
  const panel = document.getElementById('debug')
  if (!panel) return
  panel.hidden = !DEBUG_PANEL
  const version = document.getElementById('debug-version')
  if (version) version.textContent = UI_VERSION
  debugLog('ui', `ready ${UI_VERSION}`)
  debugLog('term', `native query=${location.search} bridge=${!!/** @type {any} */ (window).__dfNativeSend}`)
  debugLog('ui', navigator.userAgent)
  debugLog('ui', `viewport ${window.innerWidth}x${window.innerHeight} dpr=${window.devicePixelRatio}`)

  window.addEventListener('pagehide', () => {
    debugLog('ui', 'pagehide')
    void flushClientDebug()
  })

  // Capture phase: records the real target before any handler runs. This is
  // what tells us whether a dialog closed because a click landed outside it.
  document.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target : null
    if (!target) return
    const tag = target.tagName.toLowerCase()
    const id = target.id ? `#${target.id}` : ''
    const classes = target instanceof HTMLElement && target.className
      ? `.${target.className.trim().split(/\s+/).join('.')}`
      : ''
    const dialog = /** @type {HTMLDialogElement|null} */ (target.closest('dialog'))
    const dialogState = dialog ? `${dialog.id}${dialog.open ? ':open' : ':closed'}` : 'none'
    debugLog('click', `${tag}${id}${classes} dialog=${dialogState}`)
  }, true)

  const clear = document.getElementById('debug-clear')
  if (clear) {
    clear.addEventListener('click', () => {
      const list = document.getElementById('debug-list')
      if (list) list.replaceChildren()
    })
  }
  const close = document.getElementById('debug-close')
  if (close) close.addEventListener('click', () => { panel.hidden = true })
  const copy = document.getElementById('debug-copy')
  if (copy) {
    copy.addEventListener('click', () => {
      const text = debugText()
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(text).then(
          () => debugLog('ui', 'debug log copied'),
          () => debugLog('error', 'clipboard write failed')
        )
      }
    })
  }
  const save = document.getElementById('debug-save')
  if (save) {
    save.addEventListener('click', () => {
      const text = debugText()
      const blob = new Blob([text], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `darkfactory-debug-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`
      document.body.append(link)
      link.click()
      link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 1000)
      debugLog('ui', `debug log saved (${text.length} bytes)`)
    })
  }

  const browser = document.getElementById('repo-browser')
  if (browser) {
    new MutationObserver(() => {
      debugLog('dom', `repo-browser hidden=${browser.hidden}`)
    }).observe(browser, { attributes: true, attributeFilter: ['hidden'] })
  }
  const repoDialog = document.getElementById('dlg-repo')
  if (repoDialog instanceof HTMLDialogElement) {
    new MutationObserver(() => {
      debugLog('dom', `dlg-repo open=${repoDialog.open}`)
    }).observe(repoDialog, { attributes: true, attributeFilter: ['open'] })
  }
}

function readToken() {
  try { return sessionStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}
function saveToken(value) {
  try { sessionStorage.setItem(TOKEN_KEY, value) } catch { /* private mode */ }
}
function clearToken() {
  try { sessionStorage.removeItem(TOKEN_KEY) } catch { /* private mode */ }
}

/* ---------- device key ---------- */

/** @param {ArrayBuffer|Uint8Array} bytes */
function base64FromBytes(bytes) {
  const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
  let binary = ''
  for (let i = 0; i < view.length; i++) binary += String.fromCharCode(view[i])
  return btoa(binary)
}

function openDeviceKeyDB() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DEVICE_DB, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(DEVICE_STORE)) {
        request.result.createObjectStore(DEVICE_STORE)
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('device key storage unavailable'))
  })
}

/** @returns {Promise<any>} */
async function readDeviceKey() {
  if (!globalThis.indexedDB) return null
  let db
  try {
    db = await openDeviceKeyDB()
    return await new Promise((resolve, reject) => {
      const request = db.transaction(DEVICE_STORE, 'readonly').objectStore(DEVICE_STORE).get(DEVICE_RECORD)
      request.onsuccess = () => resolve(request.result || null)
      request.onerror = () => reject(request.error)
    })
  } catch (error) {
    debugLog('error', `device key read failed: ${error.message}`)
    return null
  } finally {
    db?.close()
  }
}

/** @param {any} record */
async function saveDeviceKey(record) {
  const db = await openDeviceKeyDB()
  try {
    await /** @type {Promise<void>} */ (new Promise((resolve, reject) => {
      const tx = db.transaction(DEVICE_STORE, 'readwrite')
      tx.objectStore(DEVICE_STORE).put(record, DEVICE_RECORD)
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
      tx.onabort = () => reject(tx.error)
    }))
  } finally {
    db.close()
  }
}

async function clearDeviceKey() {
  let db
  try {
    db = await openDeviceKeyDB()
    await /** @type {Promise<void>} */ (new Promise((resolve, reject) => {
      const tx = db.transaction(DEVICE_STORE, 'readwrite')
      tx.objectStore(DEVICE_STORE).delete(DEVICE_RECORD)
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    }))
  } catch (error) {
    debugLog('error', `device key clear failed: ${error.message}`)
  } finally {
    db?.close()
  }
}

/**
 * Raw device-auth fetch, not api(): a failed challenge is an expected fallback,
 * not proof that the current browser session was lost.
 * @param {string} path
 * @param {any} body
 * @param {boolean} [withToken]
 */
async function deviceAuthRequest(path, body, withToken = false) {
  /** @type {Record<string, string>} */
  const headers = { 'Content-Type': 'application/json', 'X-DF-Request': '1' }
  if (withToken && token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify(body)
  })
  if (!response.ok) {
    const error = new Error(`device key request failed (${response.status})`)
    // @ts-ignore error.status is intentionally available to the fallback.
    error.status = response.status
    throw error
  }
  return await response.json()
}

async function createDeviceKeyPair() {
  if (!globalThis.crypto?.subtle) throw new Error('WebCrypto is unavailable')
  // The private half is non-extractable; structured clone into IndexedDB keeps
  // it usable for signing but never readable as bytes from page script.
  const pair = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign', 'verify'])
  const spki = await crypto.subtle.exportKey('spki', pair.publicKey)
  return { privateKey: pair.privateKey, publicKey: base64FromBytes(spki) }
}

/** @param {any} record @param {string} challenge */
async function signDeviceChallenge(record, challenge) {
  const data = new TextEncoder().encode(`darkfactory-device-auth-v1\n${record.deviceId}\n${challenge}`)
  const signature = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, record.privateKey, data)
  return base64FromBytes(signature)
}

function deviceLabel() {
  const ua = navigator.userAgent
  if (/Linux/.test(ua) && /AppleWebKit/.test(ua) && !/Chrome|Chromium/.test(ua)) return 'Desktop (WebKitGTK)'
  if (/Android/.test(ua)) return 'Android browser'
  if (/iPhone|iPad|iPod/.test(ua)) return 'iOS browser'
  if (/Macintosh/.test(ua)) return 'Desktop (macOS)'
  return 'Desktop browser'
}

async function registerDeviceKey() {
  if (!token || !globalThis.crypto?.subtle || !globalThis.indexedDB) return
  if (await readDeviceKey()) return
  const pair = await createDeviceKeyPair()
  const label = deviceLabel()
  const response = await deviceAuthRequest('/api/v1/device-keys', { label, publicKey: pair.publicKey }, true)
  if (!response.deviceId) throw new Error('device key registration returned no id')
  await saveDeviceKey({
    deviceId: response.deviceId,
    privateKey: pair.privateKey,
    publicKey: pair.publicKey,
    label: response.label || label,
    createdAt: new Date().toISOString()
  })
  debugLog('ui', `device key registered ${label}`)
}

function ensureDeviceKey() {
  if (!deviceKeyRegistration) {
    deviceKeyRegistration = registerDeviceKey()
      .catch((error) => {
        debugLog('error', `device key registration failed: ${error.message}`)
      })
      .finally(() => { deviceKeyRegistration = null })
  }
  return deviceKeyRegistration
}

/**
 * Sign a server challenge and turn the result into this tab's session token.
 * A 401 means the key was revoked or the daemon state was reset, so drop the
 * stale local record and let the next token login register a fresh one.
 */
async function deviceKeyAuth() {
  if (deviceAuthPaused || deviceAuthBusy) return false
  if (!globalThis.crypto?.subtle || !globalThis.indexedDB) return false
  const record = await readDeviceKey()
  if (!record?.deviceId || !record.privateKey) return false
  deviceAuthBusy = true
  try {
    let challenge
    try {
      challenge = await deviceAuthRequest('/api/v1/device-keys/challenge', { deviceId: record.deviceId })
    } catch (error) {
      debugLog('error', `device key challenge failed: ${error.message}`)
      // A 401 here means the server no longer knows this key (revoked or a
      // reset state). The next token login should provision a fresh one. A
      // failed verify is not enough to delete it: the nonce may simply have
      // expired, and the key itself can still be valid.
      // @ts-ignore status is set by deviceAuthRequest.
      if (error.status === 401) await clearDeviceKey()
      return false
    }
    const signature = await signDeviceChallenge(record, challenge.challenge)
    const session = await deviceAuthRequest('/api/v1/device-keys/verify', {
      deviceId: record.deviceId,
      challenge: challenge.challenge,
      signature
    })
    if (!session.token) throw new Error('device key verify returned no token')
    token = session.token
    saveToken(token)
    debugLog('ui', 'device key auth ok')
    return await connect(token)
  } catch (error) {
    debugLog('error', `device key auth failed: ${error.message}`)
    return false
  } finally {
    deviceAuthBusy = false
  }
}

// Terminal preferences are not credentials; localStorage is fine here.
function readTermFont() {
  try {
    const value = Number(localStorage.getItem(TERM_FONT_KEY))
    if (Number.isFinite(value) && value >= TERM_FONT_MIN && value <= TERM_FONT_MAX) return value
  } catch { /* private mode */ }
  return TERM_FONT_DEFAULT
}
function saveTermFont(value) {
  try { localStorage.setItem(TERM_FONT_KEY, String(value)) } catch { /* private mode */ }
}

// Chat mode is per tab, not per browser: an A/B comparison means two tabs on two
// layouts, and a phone should not inherit a desktop experiment.
function readChatMode() {
  try { return sessionStorage.getItem(CHAT_MODE_KEY) === '1' } catch { return false }
}
function saveChatMode(on) {
  try { sessionStorage.setItem(CHAT_MODE_KEY, on ? '1' : '0') } catch { /* private mode */ }
}

function shortId(id) {
  return String(id || '').slice(0, 8)
}

function repoFor(id) {
  return state.repos.find((repo) => repo.id === id) || null
}
function worktreeFor(id) {
  return state.worktrees.find((wt) => wt.id === id) || null
}
function sessionFor(id) {
  return state.sessions.find((sess) => sess.id === id) || null
}
function runFor(id) {
  return state.runs.find((item) => item.id === id) || null
}

/** @param {Run} run */
function runStatePill(run) {
  const kind = run.state === 'succeeded' ? 'ok'
    : run.state === 'failed' || run.state === 'cancelled' ? 'danger'
      : run.state === 'waiting' || run.state === 'blocked' ? 'warn' : 'info'
  return pill(run.state, kind, run.state === 'running' ? 'run' : run.state === 'waiting' ? 'block' : 'idle')
}

/** @param {Step} step */
function stepStatePill(step) {
  const kind = step.state === 'succeeded' ? 'ok'
    : step.state === 'failed' ? 'danger'
      : step.state === 'waiting' || step.state === 'blocked' ? 'warn' : 'info'
  return pill(step.state, kind, step.state === 'running' ? 'run' : step.state === 'waiting' ? 'block' : 'idle')
}

function wtName(wt) {
  if (!wt) return 'worktree'
  if (wt.branch) return wt.branch.replace(/^orca\//, '')
  return (wt.path || wt.id || '').split('/').filter(Boolean).pop() || 'worktree'
}

function agentLabel(sess) {
  if (!sess) return 'agent'
  if (sess.kind === 'shell') return 'shell'
  return sess.agent || sess.kind || 'agent'
}

function timeShort(iso) {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function sessionsFor(worktreeId) {
  return state.sessions.filter((sess) => sess.worktreeId === worktreeId)
}

function latestSession(worktreeId) {
  const list = sessionsFor(worktreeId)
  if (!list.length) return null
  // The workspace row's meta follows the newest session, not the last row: a
  // pinned or dragged session can sit anywhere in the list.
  return list.reduce((newest, sess) => (
    new Date(sess.createdAt).getTime() >= new Date(newest.createdAt).getTime() ? sess : newest
  ))
}

function statusPill(sess) {
  if (!sess) return pill('none', '', 'idle')
  if (sess.live) return pill('running', 'ok', 'run')
  if (sess.status === 'lost') return pill('lost', 'danger', 'block')
  return pill(sess.status || 'exited', 'warn', 'idle')
}

/* ---------- context menu ---------- */

function hideContextMenu() {
  const menu = $('ctx-menu')
  contextTarget = null
  if (menu.hidden) return
  menu.hidden = true
  menu.replaceChildren()
}

/**
 * @param {string} label
 * @param {string} action
 * @param {{danger?: boolean, disabled?: boolean, hint?: string}} [options]
 */
function contextItem(label, action, options = {}) {
  const item = el('button', 'ctx-item', label)
  item.type = 'button'
  item.dataset.ctx = action
  if (options.danger) item.classList.add('danger')
  if (options.disabled) item.disabled = true
  if (options.hint) item.title = options.hint
  return item
}

function contextSeparator() {
  return el('div', 'ctx-sep')
}

/**
 * @param {MouseEvent} event
 * @param {{kind: 'session'|'worktree', id: string}} target
 * @param {Element|null} [anchor]
 */
function showContextMenu(event, target, anchor = null) {
  const menu = $('ctx-menu')
  menu.replaceChildren()
  contextTarget = target

  if (target.kind === 'session') {
    const sess = sessionFor(target.id)
    if (!sess) return
    menu.append(contextItem('Open session', 'open-session'))
    menu.append(contextItem('New session here', 'new-session'))
    if (!readOnly()) {
      menu.append(contextItem(sess.pinned ? 'Unpin session' : 'Pin session', 'toggle-pin', {
        hint: sess.pinned ? 'Let it sort with the other sessions again' : 'Keep this session at the top of its workspace'
      }))
    }
    menu.append(contextSeparator())
    menu.append(contextItem('Copy path', 'copy-path', { hint: 'Copy this workspace directory' }))
    menu.append(contextItem('Reveal in file manager', 'reveal-path', {
      hint: 'Open the worktree folder on the daemon machine'
    }))
    menu.append(contextSeparator())
    if (!sess.live) {
      menu.append(contextItem('Restart session', 'restart-session', {
        hint: 'Start a new PTY in this workspace; the old output is not persisted'
      }))
    }
    menu.append(contextItem(sess.live ? 'Stop session' : 'Remove session', 'remove-session', {
      danger: true,
      hint: sess.live ? 'Kill this PTY and delete the session row' : 'Delete this saved session row'
    }))
  } else {
    const wt = worktreeFor(target.id)
    if (!wt) return
    const sessions = sessionsFor(wt.id)
    const expanded = expandedWorktrees.has(wt.id)
    menu.append(contextItem(wt.workspace ? 'Open workspace' : 'Open worktree', 'open-worktree'))
    menu.append(contextItem('New session', 'new-session'))
    menu.append(contextItem(expanded ? 'Collapse sessions' : 'Expand sessions', 'toggle-expand', {
      disabled: sessions.length === 0
    }))
    menu.append(contextSeparator())
    menu.append(contextItem('Copy path', 'copy-path', { hint: 'Copy this workspace directory' }))
    menu.append(contextItem('Reveal in file manager', 'reveal-path', {
      hint: 'Open this folder on the daemon machine'
    }))
    menu.append(contextSeparator())
    if (wt.workspace) {
      menu.append(contextItem('Remove repo', 'remove-repo', {
        danger: true,
        hint: 'Stops sessions and removes the workspace record; files stay on disk'
      }))
    } else {
      menu.append(contextItem('Remove worktree', 'remove-worktree', {
        danger: true,
        hint: 'Stops sessions and removes the worktree directory; uncommitted changes are refused'
      }))
    }
  }

  event.preventDefault()
  event.stopPropagation()
  menu.hidden = false
  menu.style.left = '0px'
  menu.style.top = '0px'
  const rect = menu.getBoundingClientRect()
  const anchorRect = anchor?.getBoundingClientRect()
  const rawX = event.clientX || (anchorRect ? anchorRect.left : 12)
  const rawY = event.clientY || (anchorRect ? anchorRect.bottom : 12)
  const x = Math.max(8, Math.min(rawX, window.innerWidth - rect.width - 8))
  const y = Math.max(8, Math.min(rawY, window.innerHeight - rect.height - 8))
  menu.style.left = `${x}px`
  menu.style.top = `${y}px`
  const first = /** @type {HTMLButtonElement|null} */ (menu.querySelector('button:not(:disabled)'))
  first?.focus()
}

async function runContextAction(action) {
  const target = contextTarget
  hideContextMenu()
  if (!target) return
  if (target.kind === 'session') {
    const sess = sessionFor(target.id)
    if (!sess) return
    if (action === 'open-session') void openSession(sess)
    if (action === 'new-session') openSessionDialog(sess.worktreeId)
    if (action === 'restart-session') void restartSession(sess)
    if (action === 'remove-session') void removeSession(sess)
    if (action === 'toggle-pin') void togglePinSession(sess)
    if (action === 'copy-path') void copyText(worktreeFor(sess.worktreeId)?.path || '', 'Path')
    if (action === 'reveal-path') void revealWorktree(sess.worktreeId)
    return
  }
  const wt = worktreeFor(target.id)
  if (!wt) return
  if (action === 'open-worktree') selectWorktree(wt.id)
  if (action === 'new-session') openSessionDialog(wt.id)
  if (action === 'copy-path') void copyText(wt.path || '', 'Path')
  if (action === 'reveal-path') void revealWorktree(wt.id)
  if (action === 'toggle-expand') {
    if (expandedWorktrees.has(wt.id)) expandedWorktrees.delete(wt.id)
    else expandedWorktrees.add(wt.id)
    renderTree()
  }
  if (action === 'remove-worktree') void removeWorktree(wt)
  if (action === 'remove-repo') {
    const repo = repoFor(wt.repoId)
    if (repo) void removeRepo(repo)
  }
}

// Pin is server state like the fleet order, so a reload and the phone agree.
/** @param {Session} sess */
async function togglePinSession(sess) {
  if (!sess || readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  const next = !sess.pinned
  try {
    await api(`/api/v1/sessions/${encodeURIComponent(sess.id)}/pin`, { method: 'POST', body: { pinned: next } })
    await refresh()
    toast(next ? 'Pinned to the top.' : 'Unpinned.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

// The daemon opens the folder with the desktop's own file manager, so this only
// works from the machine that runs it; a remote client gets a refusal that says
// exactly that.
/** @param {string} worktreeId */
async function revealWorktree(worktreeId) {
  const wt = worktreeFor(worktreeId)
  if (!wt) return
  try {
    await api(`/api/v1/worktrees/${encodeURIComponent(wt.id)}/reveal`, { method: 'POST', body: {} })
    toast('Opened in the file manager.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

function toast(message, bad) {
  const node = $('toast')
  node.textContent = message
  node.classList.toggle('bad', Boolean(bad))
  node.classList.add('on')
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => node.classList.remove('on'), 3200)
}

async function copyText(value, label) {
  const text = String(value || '')
  if (!text) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      toast(`${label} copied.`)
      return
    }
  } catch { /* fall through to the selection fallback */ }
  const area = document.createElement('textarea')
  area.value = text
  area.className = 'copy-helper'
  area.setAttribute('readonly', '')
  document.body.append(area)
  area.select()
  try {
    if (document.execCommand('copy')) toast(`${label} copied.`)
    else toast('Copy failed.', true)
  } catch {
    toast('Copy failed.', true)
  }
  area.remove()
}

function setConn(kind) {
  const changed = connState !== kind
  connState = kind
  const dotNode = $('conn-dot')
  dotNode.className = `dot ${kind === 'connected' ? 'run' : kind === 'offline' ? 'block' : 'idle'}`
  $('conn-text').textContent = kind === 'connected' ? 'connected' : kind === 'offline' ? 'offline' : 'connecting'
  const pillNode = $('conn-pill')
  const title = kind === 'connected'
    ? 'Refresh daemon state'
    : kind === 'offline' ? 'Daemon offline — click to reconnect' : 'Connecting to the daemon'
  pillNode.title = title
  pillNode.setAttribute('aria-label', title)
  if (changed && currentSession && selection?.kind === 'session') renderSession()
}

function setBusy(form, busy) {
  const button = form.querySelector('button[type="submit"]')
  if (button) button.disabled = busy
  form.setAttribute('aria-busy', String(busy))
}

function isBusy(form) {
  return form.getAttribute('aria-busy') === 'true'
}

/* ---------- HTTP ---------- */

/**
 * The daemon answers a failure with a refusal envelope (GV-7): a stable code,
 * an optional detail and a hint. Old or proxied responses may still be plain
 * text, so this falls back to the raw body.
 * @param {Response} response
 * @returns {Promise<{code: string, detail: string, hint: string, target: string, rule: string, remediable: boolean}>}
 */
async function refusalFrom(response) {
  const raw = (await response.text()).trim()
  const fallback = { code: '', detail: raw, hint: '', target: '', rule: '', remediable: false }
  if (!raw) return fallback
  try {
    const body = JSON.parse(raw)
    if (!body || typeof body !== 'object' || typeof body.code !== 'string') return fallback
    return {
      code: body.code,
      detail: typeof body.detail === 'string' ? body.detail : '',
      hint: typeof body.hint === 'string' ? body.hint : '',
      target: typeof body.target === 'string' ? body.target : '',
      rule: typeof body.rule === 'string' ? body.rule : '',
      remediable: body.remediable === true
    }
  } catch {
    return fallback
  }
}

/**
 * @param {{code?: string, detail?: string, hint?: string}} refusal
 * @param {string} [fallback]
 * @returns {string}
 */
function refusalMessage(refusal, fallback = 'Request failed.') {
  return refusal.detail || refusal.hint || refusal.code || fallback
}

/**
 * @param {string} scope
 * @returns {string} an opaque key for one launch intent
 */
function newRequestKey(scope) {
  const uuid = globalThis.crypto && typeof globalThis.crypto.randomUUID === 'function'
    ? globalThis.crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
  return `${scope}-${uuid}`
}

/**
 * @param {string} path
 * @param {{method?: string, headers?: HeadersInit, body?: any, signal?: AbortSignal, cache?: RequestCache}} [options]
 * @returns {Promise<any>}
 */
async function api(path, options = {}) {
  const init = /** @type {RequestInit} */ ({ ...options, credentials: 'same-origin' })
  const headers = new Headers(init.headers || {})
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (init.body !== undefined && typeof init.body !== 'string') {
    headers.set('Content-Type', 'application/json')
    init.body = JSON.stringify(init.body)
  }
  const method = String(init.method || 'GET').toUpperCase()
  // A 401 only means "this browser lost its session" when it answers a request
  // that used the session we still believe in. The cold-load cookie probe and a
  // connect attempt are unauthenticated by definition, and an in-flight probe
  // that answers late must not clear the token the user just pasted.
  const requestToken = token
  // SameSite=Strict blocks cross-site cookies; this header is the second gate
  // for a same-site request forged by another local page.
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    headers.set('X-DF-Request', '1')
  }
  init.headers = headers
  debugLog('api', `→ ${method} ${path}`)
  let response
  try {
    response = await fetch(path, init)
  } catch {
    debugLog('error', `fetch ${method} ${path} failed`)
    throw new Error('daemon unreachable')
  }
  debugLog('api', `← ${response.status} ${method} ${path}`)
  if (response.status === 401) {
    if (authenticated && requestToken === token) {
      debugLog('error', `401 ${method} ${path} -> lostAuth`)
      lostAuth()
    }
    throw new Error('unauthorized')
  }
  if (!response.ok) {
    const refusal = await refusalFrom(response)
    const text = refusalMessage(refusal, response.statusText)
    debugLog('error', `${response.status} ${method} ${path}: ${refusal.code || text.slice(0, 120)}`)
    const failure = new globalThis.Error(text.length > 400 ? `${text.slice(0, 400)}…` : text)
    const tagged = /** @type {{status?: number, code?: string}} */ (failure)
    tagged.status = response.status
    tagged.code = refusal.code || ''
    throw failure
  }
  if (response.status === 204) return null
  return response.json()
}

function closeAllDialogs() {
  const dialogs = /** @type {NodeListOf<HTMLDialogElement>} */ (document.querySelectorAll('dialog[open]'))
  debugLog('dialog', `closeAllDialogs count=${dialogs.length}`)
  dialogs.forEach((dialog) => dialog.close())
}

function lostAuth() {
  debugLog('error', `lostAuth: closing every dialog and returning to Connect caller=${caller()}`)
  token = ''
  authenticated = false
  clearToken()
  refreshSeq++
  connectSeq++
  stopStream()
  stopOpenCodePolling()
  stopPolling()
  clearAttention()
  closeAllDialogs()
  currentSession = null
  selection = null
  openCodeSessions = []
  openCodeSessionId = ''
  openCodeMessages = []
  openCodeListSignature = ''
  openCodeMessageSignature = ''
  openCodeOlderCursor = ''
  openCodeLoadedOlder = false
  openCodePermissions = []
  openCodeSearch = ''
  terminalSessionId = ''
  exitZen()
  resetTerminal()
  if (terminal) terminal.options.disableStdin = true
  setConn('offline')
  $('app').hidden = true
  $('connect').hidden = false
  $('connect-error').textContent = isMobile()
    ? 'No browser session. Enter the pairing code from Pair phone, or paste a token.'
    : 'No browser session. Run darkfactory open, pair this device, or paste a token.'
  inputEl('token').value = ''
  if (isMobile()) inputEl('pair-input').focus()
  else inputEl('token').focus()
  // A rotated daemon secret or restart drops the in-memory session token. The
  // remembered device key can mint a new one without asking the user to paste
  // the master token again. A deliberate Disconnect sets the pause flag above.
  if (!deviceAuthPaused && !deviceAuthBusy) void deviceKeyAuth()
}

/* ---------- connect ---------- */

async function connect(candidate) {
  // The cold-load cookie probe runs while the user may already be pasting a
  // token. Only the newest attempt may decide the outcome: an older probe's
  // answer must not clear the newest token, hide the app, or overwrite the
  // error the newest attempt produced.
  const seq = ++connectSeq
  token = String(candidate || '').trim()
  if (token) deviceAuthPaused = false
  setConn('connecting')
  debugLog('ui', `connect token length=${token.length}`)
  try {
    const next = await api('/api/v1/state')
    if (seq !== connectSeq) return false
    if (token) {
      saveToken(token)
      // First master-token login provisions this device. Later launches use
      // the key instead; registration is best effort and never blocks the UI.
      void ensureDeviceKey()
    }
    authenticated = true
    state = normalizeState(next)
    attentionEvents = []
    // Cursor 0 asks for the ring the daemon still holds: activity that happened
    // before this page loaded is what the owner wanted to see.
    attentionCursor = 0
    renderAttention()
    $('connect').hidden = true
    $('app').hidden = false
    $('connect-error').textContent = ''
    inputEl('token').value = ''
    setConn('connected')
    debugLog('ui', 'connect ok')
    boot()
    return true
  } catch (error) {
    if (seq !== connectSeq) return false
    debugLog('error', `connect failed: ${error.message}`)
    token = ''
    authenticated = false
    setConn('offline')
    $('connect-error').textContent = error.message === 'unauthorized'
      ? 'No browser session. Pair this device, run darkfactory open, or paste a token.'
      : error.message
    $('app').hidden = true
    $('connect').hidden = false
    return false
  }
}

async function pairDevice(candidate) {
  const code = String(candidate || '').trim()
  if (!code) {
    $('connect-error').textContent = 'Enter the pairing code from the desktop.'
    return false
  }
  setConn('connecting')
  debugLog('ui', `pair code length=${code.length}`)
  let response
  try {
    response = await fetch('/api/v1/pair', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-DF-Request': '1' },
      body: JSON.stringify({ code })
    })
  } catch {
    debugLog('error', 'pair fetch failed')
    setConn('offline')
    $('connect-error').textContent = 'Daemon unreachable.'
    $('app').hidden = true
    $('connect').hidden = false
    return false
  }
  if (!response.ok) {
    const refusal = await refusalFrom(response)
    debugLog('error', `pair failed ${response.status} ${refusal.code || refusal.detail.slice(0, 80)}`)
    setConn('offline')
    $('connect-error').textContent = response.status === 429
      ? 'Too many pairing attempts. Wait a minute and try again.'
      : refusal.code === 'pair_https_only'
        ? 'Pairing a remote device requires HTTPS. Use Tailscale Serve or an SSH tunnel.'
        : response.status === 401
          ? 'That pairing code is invalid or expired. Ask the desktop for a new code.'
          : response.status === 403
            ? 'Pairing was refused by the daemon. Check that this hostname is in allowedHosts.'
            : refusalMessage(refusal, 'Pairing failed.')
    $('app').hidden = true
    $('connect').hidden = false
    inputEl('pair-input').value = code
    inputEl('pair-input').focus()
    return false
  }
  token = ''
  clearToken()
  return await connect('')
}

async function pairFromConnect() {
  if (pairBusy || connectBusy) return
  pairBusy = true
  const form = formEl('connect-form')
  const button = buttonEl('btn-pair-connect')
  button.disabled = true
  setBusy(form, true)
  $('connect-error').textContent = ''
  const ok = await pairDevice(inputEl('pair-input').value)
  setBusy(form, false)
  button.disabled = false
  pairBusy = false
  if (ok) {
    if (currentSession && terminal) terminal.focus()
  } else {
    inputEl('pair-input').select()
  }
}

async function disconnect() {
  // Stop the automatic retry before clearing the session, otherwise a 401 from
  // logout could sign the device key and connect again immediately.
  deviceAuthPaused = true
  try {
    await api('/api/v1/open/logout', { method: 'POST' })
  } catch { /* already disconnected or offline */ }
  token = ''
  authenticated = false
  clearToken()
  refreshSeq++
  stopStream()
  stopOpenCodePolling()
  stopPolling()
  clearAttention()
  closeAllDialogs()
  currentSession = null
  selection = null
  openCodeSessions = []
  openCodeSessionId = ''
  openCodeMessages = []
  openCodeListSignature = ''
  openCodeMessageSignature = ''
  openCodeOlderCursor = ''
  openCodeLoadedOlder = false
  openCodePermissions = []
  openCodeSearch = ''
  expandedWorktrees.clear()
  hideContextMenu()
  terminalSessionId = ''
  exitZen()
  resetTerminal()
  if (terminal) terminal.options.disableStdin = true
  state = { repos: [], worktrees: [], sessions: [], runs: [], agents: {}, opencodeURL: '', opencode: false, scope: 'full', build: '', ui: '' }
  $('app').hidden = true
  $('connect').hidden = false
  $('connect-error').textContent = ''
  inputEl('token').value = ''
  inputEl('pair-input').value = ''
  setConn('offline')
  if (isMobile()) inputEl('pair-input').focus()
  else inputEl('token').focus()
}

/** Read a one-time code from the URL fragment, then remove it from history. */
function takeFragment(name) {
  const fragment = location.hash.startsWith('#') ? location.hash.slice(1) : ''
  if (!fragment) return ''
  const code = new URLSearchParams(fragment).get(name) || ''
  if (code) {
    history.replaceState(null, '', location.pathname + location.search)
    debugLog('ui', `consumed ${name} fragment`)
  }
  return code
}

function takeOpenCode() {
  return takeFragment('code')
}

function takePairCode() {
  return takeFragment('pair')
}

function boot() {
  render()
  if (!selection) pickInitial()
  // The phone starts on Fleet, but not when the panel is what was asked for:
  // `.app[data-mobile="fleet"] .main { display: none }` would hide it.
  if (isMobile() && selection?.kind !== 'opencode') setMobileView('fleet')
  startPolling()
  // A restored session is opened before the first layout frame; give the
  // canvas one measured fit after the app becomes visible.
  refreshTerminalSoon()
}

function pickInitial() {
  // `?chat=1` and `?chat=0` both exist to compare the two layouts, so either one
  // opens the panel they are comparing. A remembered chat-mode tab reopens on
  // the chat it exists for; otherwise the desktop reopens the newest live PTY.
  if (state.opencode && (chatMode || CHAT_QUERY === '0')) {
    // A remembered chat tab still needs a worktree; use the same fallback the
    // fleet uses so the panel never opens without a scope.
    const liveWorktree = state.sessions.filter((sess) => sess.live).map((sess) => sess.worktreeId)
    const scoped = [
      scopedWorktreeId(),
      liveWorktree[liveWorktree.length - 1],
      state.worktrees.find((wt) => wt.workspace)?.id,
      state.worktrees[state.worktrees.length - 1]?.id
    ].find((id) => id && worktreeFor(id))
    if (scoped) {
      showOpenCode(scoped)
      return
    }
  }
  const live = state.sessions.filter((sess) => sess.live)
  if (!isMobile() && live.length) {
    void openSession(live[live.length - 1])
    return
  }
  const checkout = state.worktrees.find((wt) => wt.workspace)
  if (checkout) {
    selection = { kind: 'worktree', id: checkout.id }
  } else if (state.worktrees.length) {
    selection = { kind: 'worktree', id: state.worktrees[state.worktrees.length - 1].id }
  } else {
    selection = null
  }
  renderView()
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(() => {
    if (!document.hidden) void refresh()
  }, POLL_MS)
  void pollNotifications()
  attentionTimer = window.setInterval(() => {
    if (!document.hidden) void pollNotifications()
  }, ATTENTION_POLL_MS)
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) void pollNotifications()
  })
}
function stopPolling() {
  if (pollTimer) window.clearInterval(pollTimer)
  if (attentionTimer) window.clearInterval(attentionTimer)
  pollTimer = 0
  attentionTimer = 0
}

async function refresh(options = {}) {
  const silent = options.silent !== false
  if (!authenticated) return
  const seq = ++refreshSeq
  try {
    const next = normalizeState(await api('/api/v1/state'))
    // A slower, older response must not overwrite newer state.
    if (seq !== refreshSeq) return
    const changed = JSON.stringify(next) !== JSON.stringify(state)
    const wasConnected = connState === 'connected'
    state = next
    setConn('connected')
    // Do not rebuild the fleet and the active view every poll: on iPad that
    // resets the scroll position and makes the list feel like it is sliding.
    // Re-render on a real state change, or once when the connection returns.
    if (changed || !wasConnected) render()
  } catch (error) {
    if (error.message !== 'unauthorized') {
      setConn('offline')
      if (!silent) toast(error.message, true)
    }
  }
}

async function reconnect() {
  if (!authenticated || connState === 'connecting') return
  setConn('connecting')
  await refresh({ silent: false })
  if (connState !== 'connected') return
  if (currentSession?.live && !streaming) {
    void openSession(currentSession)
  }
  toast('Reconnected.')
}

/* ---------- rendering ---------- */

function count(value, noun) {
  return `${value} ${noun}${value === 1 ? '' : 's'}`
}

function render() {
  const live = state.sessions.filter((sess) => sess.live).length
  $('status-counts').textContent =
    `${count(state.repos.length, 'repo')} · ${count(state.worktrees.length, 'worktree')} · ${count(state.sessions.length, 'session')}` +
    (live ? ` (${live} live)` : '')

  buttonEl('btn-pair').disabled = readOnly()
  buttonEl('btn-pair').title = readOnly() ? 'This device is read-only' : 'Pair a phone'
  // Chat is entered from a worktree or session, never as a destination of its
  // own: a chat without a directory has nothing to scope to.
  buttonEl('btn-wt-chat').hidden = !state.opencode
  buttonEl('btn-sess-chat').hidden = !state.opencode
  // The phone hides the view head on a live session; this is the same Chat
  // action in the terminal header, so the panel is still one tap away.
  buttonEl('btn-term-chat').hidden = !state.opencode || selection?.kind !== 'session'
  // A read-only device's new PTY could only be refused by the daemon.
  buttonEl('btn-wt-new-session').hidden = !hasTerminal()
  buttonEl('btn-empty-session').hidden = !hasTerminal()

  const modes = new Set(state.worktrees.map((wt) => wt.containment || 'off'))
  const containment = $('status-containment')
  if (modes.size === 1 && modes.has('jail')) {
    containment.textContent = 'jail · daemon state masked'
    containment.title = 'Per-worktree HOME and bubblewrap mask; the agent still runs as your uid.'
  } else if (modes.size > 1) {
    containment.textContent = 'mixed containment'
    containment.title = 'Some worktrees are jailed, some run as the same uid.'
  } else {
    containment.textContent = 'same-uid · not a sandbox'
    containment.title = 'The daemon and agents share your uid.'
  }

  syncCurrent()
  renderTree()
  renderView()
  syncUiVersion()
}

// The footer names both halves: the browser constant that changed and the
// daemon build serving it. A stale binary or cached app.js is then visible.
function syncUiVersion() {
  reloadIfStaleUi()
  const footer = document.getElementById('ui-version')
  if (!footer) return
  footer.textContent = state.build ? `ui ${UI_VERSION} · ${state.build}` : `ui ${UI_VERSION}`
  footer.title = state.build
    ? `browser ${UI_VERSION}; daemon ${state.build}`
    : `browser ${UI_VERSION}; daemon did not report a build`
}

// A window opened before an install keeps the app.js it already ran, even
// though the daemon serves the new shell with no-store. The daemon reports the
// UI it embeds; when that differs, ask this origin what a reload would actually
// load. That distinction matters on the phone: the APK serves its own copy of
// the shell, so the daemon being newer must not make the page throw away a
// working app. Reload only when the served shell is also different, once per
// target version (a cache that ignores no-store, or a rollback, must not loop).
let staleUiCheck = false

function reloadIfStaleUi() {
  if (!state.ui || state.ui === UI_VERSION || staleUiCheck) return
  const key = 'df-ui-reload'
  try {
    if (sessionStorage.getItem(key) === state.ui) return
  } catch {
    return
  }
  staleUiCheck = true
  void (async () => {
    try {
      const served = await servedShellVersion()
      // No answer (offline, odd proxy): try again on the next state poll.
      if (!served) return
      try {
        sessionStorage.setItem(key, state.ui)
      } catch {
        return
      }
      if (served === UI_VERSION) return
      debugLog('ui', `reloading ${UI_VERSION} for served shell ${served}`)
      location.reload()
    } finally {
      staleUiCheck = false
    }
  })()
}

// servedShellVersion is the UI version of the shell this origin would hand a
// reload: the daemon in a browser or the desktop window, the APK on a phone.
async function servedShellVersion() {
  try {
    const response = await fetch('/', { cache: 'no-store', credentials: 'same-origin' })
    if (!response.ok) return ''
    const match = (await response.text()).match(/app\.js\?v=([^"'&]+)/)
    return match ? match[1] : ''
  } catch {
    return ''
  }
}

function syncCurrent() {
  if (!currentSession) return
  const found = sessionFor(currentSession.id)
  if (found) {
    currentSession = found
    return
  }
  const worktreeId = currentSession.worktreeId
  stopStream()
  currentSession = null
  terminalSessionId = ''
  resetTerminal()
  if (terminal) terminal.options.disableStdin = true
  if (selection?.kind === 'session') {
    selection = worktreeFor(worktreeId) ? { kind: 'worktree', id: worktreeId } : null
  }
}

function renderTree() {
  const tree = $('tree')
  // A poll must not rebuild the list under a finger mid-drag: the pointer
  // handlers hold the row elements and the drop indicator lives on them. The
  // guard covers the press itself, not only the six-pixel threshold, because a
  // slow press on a phone can outlive a one-second poll.
  if (treeDrag) return
  const scrollTop = tree.scrollTop
  tree.replaceChildren()

  if (!state.repos.length) {
    tree.append(el('div', 'tree-empty', 'No repos yet. Add one to start.'))
    tree.scrollTop = scrollTop
    return
  }

  const activeWorktree = selection?.kind === 'session'
    ? currentSession?.worktreeId
    : selection?.kind === 'worktree'
      ? selection.id
      : selection?.kind === 'opencode' ? openCodeScopeWorktreeId : null

  for (const repo of state.repos) {
    const worktrees = state.worktrees.filter((wt) => wt.repoId === repo.id)
    const block = el('div', 'repo')
    block.dataset.repo = repo.id
    const head = el('div', 'repo-head')
    // A repo is a fleet group: its grip moves the whole group (header,
    // workspaces and sessions). Without it, a fleet of one-workspace repos has
    // no row that can move, which is the common layout.
    if (!readOnly()) {
      const grip = el('button', 'repo-grip', '⠿')
      grip.type = 'button'
      grip.dataset.repoGrip = repo.id
      grip.title = 'Drag to reorder this repo'
      grip.setAttribute('aria-label', `Reorder ${repo.name}; arrow keys move it up or down`)
      head.append(grip)
    }
    head.append(el('span', 'repo-name', repo.name))
    if (!worktrees.length) {
      const remove = el('button', 'icon-btn', '×')
      remove.type = 'button'
      remove.dataset.repoDel = repo.id
      remove.title = 'Remove repo'
      remove.setAttribute('aria-label', `Remove repo ${repo.name}`)
      head.append(remove)
    }
    block.append(head)

    if (!worktrees.length) {
      const create = el('button', 'repo-empty repo-new', repo.git ? 'Open checkout' : 'Open workspace')
      create.type = 'button'
      create.dataset.repoWorkspace = repo.id
      create.title = `Open ${repo.path} as an in-place ${repo.git ? 'checkout' : 'workspace'}`
      block.append(create)
    } else {
      const list = el('ul', 'wt-list')
      for (const wt of worktrees) {
        const item = el('li', 'wt-item')
        item.dataset.wtItem = wt.id
        const row = el('div', 'wt-row')
        const sessions = sessionsFor(wt.id)
        const expanded = expandedWorktrees.has(wt.id)

        // The grip is the only drag handle: a touch that starts on the row has
        // to keep scrolling the sidebar (UI-23). A repo with one workspace has
        // no sibling to swap with, so its row grip moves the whole repo group.
        if (!readOnly()) {
          const grip = el('button', 'wt-grip', '⠿')
          grip.type = 'button'
          grip.dataset.wtGrip = wt.id
          const singleGroup = worktrees.length === 1
          grip.title = singleGroup ? 'Drag to reorder this repo' : 'Drag to reorder inside this repo'
          grip.setAttribute('aria-label', `Reorder ${wtName(wt)}; arrow keys move it up or down`)
          row.append(grip)
        }

        const toggle = el('button', 'wt-toggle', expanded ? '▾' : '▸')
        toggle.type = 'button'
        toggle.dataset.wtToggle = wt.id
        toggle.setAttribute('aria-expanded', String(expanded))
        toggle.setAttribute('aria-label', `${expanded ? 'Collapse' : 'Expand'} sessions for ${wtName(wt)}`)
        toggle.title = expanded ? 'Collapse sessions' : 'Expand sessions'
        if (!sessions.length) toggle.disabled = true
        row.append(toggle)

        const button = el('button', 'wt')
        button.type = 'button'
        button.dataset.wt = wt.id
        if (wt.id === activeWorktree) button.classList.add('on')
        button.title = `${repo.name} / ${wtName(wt)} · ${wt.path}`
        button.append(el('span', 'wt-name', wtName(wt)))

        const latest = latestSession(wt.id)
        const meta = el('span', 'wt-meta')
        if (latest) {
          meta.append(sessionActivity(latest))
          meta.append(el('span', '', `${agentLabel(latest)} · ${latest.live ? 'running' : latest.status}`))
        } else {
          meta.append(dot('idle'))
          meta.append(el('span', '', 'no sessions'))
        }
        button.append(meta)
        row.append(button)

        const add = el('button', 'wt-add', '+')
        add.type = 'button'
        add.dataset.wtNewSession = wt.id
        add.title = `New session in ${wt.workspace ? 'this checkout' : 'this worktree'}`
        add.setAttribute('aria-label', `New session in ${wtName(wt)}`)
        row.append(add)
        item.append(row)

        if (expanded) {
          const sessionList = el('ul', 'tree-sessions')
          if (!sessions.length) {
            sessionList.append(el('li', 'tree-session-empty', 'No sessions'))
          } else {
            for (const sess of sessions) {
              const li = el('li')
              const line = el('div', 'tree-session')
              if (sess.id === currentSession?.id) line.classList.add('on')
              if (sess.pinned) li.classList.add('pinned')
              line.dataset.treeSession = sess.id

              // Only the grip reorders; the row itself keeps scrolling the
              // sidebar on touch (UI-23). A workspace with one session has
              // nothing to swap with, so it gets no handle.
              if (!readOnly() && sessions.length > 1) {
                const grip = el('button', 'session-grip', '⠿')
                grip.type = 'button'
                grip.dataset.sessionGrip = sess.id
                grip.title = 'Drag to reorder this session'
                grip.setAttribute('aria-label', `Reorder ${sess.title || agentLabel(sess)}; arrow keys move it up or down`)
                line.append(grip)
              }

              const open = el('button', 'tree-session-main')
              open.type = 'button'
              open.dataset.treeSessionOpen = sess.id
              open.title = `${sess.title || agentLabel(sess)} · ${sess.status} · ${sess.id}`
              open.append(sessionActivity(sess))
              if (sess.pinned) {
                const pin = el('span', 'tree-session-pin', '⚑')
                pin.title = 'Pinned to the top of this workspace'
                pin.setAttribute('aria-label', 'Pinned')
                open.append(pin)
              }
              const text = el('span', 'tree-session-text')
              text.append(el('span', 'tree-session-title', sess.title || agentLabel(sess)))
              text.append(el('span', 'tree-session-meta', `${sess.live ? 'running' : sess.status} · ${shortId(sess.id)}`))
              open.append(text)
              line.append(open)

              const kill = el('button', 'tree-session-x', '×')
              kill.type = 'button'
              kill.dataset.treeSessionKill = sess.id
              kill.title = sess.live ? 'Stop session' : 'Remove session'
              kill.setAttribute('aria-label', `${sess.live ? 'Stop' : 'Remove'} ${sess.title || agentLabel(sess)}`)
              line.append(kill)

              li.append(line)
              sessionList.append(li)
            }
          }
          item.append(sessionList)
        }
        list.append(item)
      }
      block.append(list)
    }
    tree.append(block)
  }
  tree.scrollTop = scrollTop
}

/* ---------- fleet order ---------- */

// The fleet has two orders, both server state. Dragging a repo grip moves the
// whole repo group (Repo.Position); dragging a workspace grip moves that row
// inside its repo (Worktree.Position). A repo with exactly one workspace has no
// sibling to swap with, so its row grip moves the group instead; that is the
// common "one checkout per repo" layout, and without it the row would be a
// dead handle. Drops reorder the local list first so the row does not snap back
// while the POST is in flight.
let treeDrag = null

/** @param {string} repoId */
function repoBlockFor(repoId) {
  const tree = $('tree')
  const children = /** @type {HTMLElement[]} */ (Array.from(tree.children))
  return children.find((node) => node.dataset.repo === repoId) || null
}

/** @param {HTMLUListElement} list @param {HTMLLIElement} skip */
function worktreeRowIDs(list, skip) {
  const nodes = /** @type {HTMLElement[]} */ (Array.from(list.querySelectorAll('[data-wt]')))
  return nodes
    .filter((node) => !skip || !skip.contains(node))
    .map((node) => node.dataset.wt)
    .filter((id) => Boolean(id))
}

/** @param {HTMLElement} list @param {HTMLElement} skip */
function repoRowIDs(list, skip) {
  const children = /** @type {HTMLElement[]} */ (Array.from(list.children))
  return children
    .filter((node) => node.classList.contains('repo') && node !== skip)
    .map((node) => node.dataset.repo)
    .filter((id) => Boolean(id))
}

/** @param {string} repoId @param {string[]} ids */
function applyWorktreeOrder(repoId, ids) {
  const byID = new Map(state.worktrees.map((wt) => [wt.id, wt]))
  const moved = ids.map((id) => byID.get(id)).filter(Boolean)
  if (!moved.length) return
  const firstAt = state.worktrees.findIndex((wt) => wt.repoId === repoId)
  if (firstAt < 0) return
  const others = state.worktrees.filter((wt) => wt.repoId !== repoId)
  moved.forEach((wt, index) => {
    wt.position = index + 1
  })
  state.worktrees = [...others.slice(0, firstAt), ...moved, ...others.slice(firstAt)]
}

/** @param {string[]} ids */
function applyRepoOrder(ids) {
  const byID = new Map(state.repos.map((repo) => [repo.id, repo]))
  const moved = ids.map((id) => byID.get(id)).filter(Boolean)
  if (!moved.length) return
  // Keep a repo the caller did not name: it is a stale list, not permission to
  // drop a row. The server appends unnamed repos by age for the same reason.
  for (const repo of state.repos) {
    if (!moved.includes(repo)) moved.push(repo)
  }
  moved.forEach((repo, index) => {
    repo.position = index + 1
  })
  state.repos = moved
}

/** @param {HTMLElement} list */
function clearDropMarks(list) {
  for (const row of list.children) row.classList.remove('drop-before', 'drop-after')
}

/** @param {string} repoId @param {string[]} ids */
async function submitWorktreeOrder(repoId, ids) {
  const before = state.worktrees
  applyWorktreeOrder(repoId, ids)
  renderTree()
  debugLog('ui', `reorder ${repoId}: ${ids.join(',')}`)
  try {
    await api('/api/v1/worktrees/order', { method: 'POST', body: { repoId, ids } })
  } catch (error) {
    state.worktrees = before
    renderTree()
    toast(error.message, true)
  }
}

/** @param {string[]} ids */
async function submitRepoOrder(ids) {
  const before = state.repos
  applyRepoOrder(ids)
  renderTree()
  debugLog('ui', `repo order: ${ids.join(',')}`)
  try {
    await api('/api/v1/repos/order', { method: 'POST', body: { ids } })
  } catch (error) {
    state.repos = before
    renderTree()
    toast(error.message, true)
  }
}

/** @param {string} id @param {number} delta @param {string} [focusWorktree] */
function moveRepo(id, delta, focusWorktree) {
  const ids = state.repos.map((repo) => repo.id)
  const at = ids.indexOf(id)
  const to = at + delta
  if (at < 0 || to < 0 || to >= ids.length) return
  ids.splice(at, 1)
  ids.splice(to, 0, id)
  void submitRepoOrder(ids)
  window.requestAnimationFrame(() => {
    const selector = focusWorktree ? `[data-wt-grip="${focusWorktree}"]` : `[data-repo-grip="${id}"]`
    const next = /** @type {HTMLElement|null} */ (document.querySelector(selector))
    next?.focus()
  })
}

/** @param {string} worktreeId @param {number} delta */
function moveWorktree(worktreeId, delta) {
  const wt = worktreeFor(worktreeId)
  if (!wt) return
  const ids = state.worktrees.filter((row) => row.repoId === wt.repoId).map((row) => row.id)
  const at = ids.indexOf(worktreeId)
  const to = at + delta
  if (at < 0 || to < 0 || to >= ids.length) return
  ids.splice(at, 1)
  ids.splice(to, 0, worktreeId)
  void submitWorktreeOrder(wt.repoId, ids)
  window.requestAnimationFrame(() => {
    const next = /** @type {HTMLElement|null} */ (document.querySelector(`[data-wt-grip="${worktreeId}"]`))
    next?.focus()
  })
}

/** @param {string} worktreeId @param {string[]} ids */
function applySessionOrder(worktreeId, ids) {
  const byID = new Map(state.sessions.map((sess) => [sess.id, sess]))
  const moved = ids.map((id) => byID.get(id)).filter(Boolean)
  if (!moved.length) return
  const firstAt = state.sessions.findIndex((sess) => sess.worktreeId === worktreeId)
  if (firstAt < 0) return
  const others = state.sessions.filter((sess) => sess.worktreeId !== worktreeId)
  moved.forEach((sess, index) => {
    sess.position = index + 1
  })
  state.sessions = [...others.slice(0, firstAt), ...moved, ...others.slice(firstAt)]
}

/** @param {string} worktreeId @param {string[]} ids */
async function submitSessionOrder(worktreeId, ids) {
  const before = state.sessions
  applySessionOrder(worktreeId, ids)
  renderTree()
  debugLog('ui', `session order ${worktreeId}: ${ids.join(',')}`)
  try {
    await api('/api/v1/sessions/order', { method: 'POST', body: { worktreeId, ids } })
  } catch (error) {
    state.sessions = before
    renderTree()
    toast(error.message, true)
  }
}

/** @param {string} sessionId @param {number} delta */
function moveSession(sessionId, delta) {
  const sess = sessionFor(sessionId)
  if (!sess) return
  const all = sessionsFor(sess.worktreeId)
  const group = all.filter((row) => Boolean(row.pinned) === Boolean(sess.pinned))
  const ids = group.map((row) => row.id)
  const at = ids.indexOf(sessionId)
  const to = at + delta
  if (at < 0 || to < 0 || to >= ids.length) return
  ids.splice(at, 1)
  ids.splice(to, 0, sessionId)
  const pinnedIDs = sess.pinned ? ids : all.filter((row) => row.pinned).map((row) => row.id)
  const looseIDs = sess.pinned ? all.filter((row) => !row.pinned).map((row) => row.id) : ids
  void submitSessionOrder(sess.worktreeId, pinnedIDs.concat(looseIDs))
  window.requestAnimationFrame(() => {
    const next = /** @type {HTMLElement|null} */ (document.querySelector(`[data-session-grip="${sessionId}"]`))
    next?.focus()
  })
}

/** @param {'repo'|'worktree'|'session'} kind @param {string} id @param {string} scopeId @param {HTMLElement} item @param {HTMLElement} list @param {number} startY */
function startTreeDrag(kind, id, scopeId, item, list, startY) {
  treeDrag = { kind, id, scopeId, startY, started: false, item, list, insertAt: -1 }
}

/** @param {boolean} commit */
function finishTreeDrag(commit) {
  const drag = treeDrag
  treeDrag = null
  if (!drag) return
  document.body.classList.remove('wt-dragging')
  drag.item.classList.remove('dragging')
  clearDropMarks(drag.list)
  if (!commit || !drag.started || drag.insertAt < 0) return
  if (drag.kind === 'repo') {
    const ids = repoRowIDs(drag.list, drag.item)
    ids.splice(drag.insertAt, 0, drag.id)
    void submitRepoOrder(ids)
    return
  }
  if (drag.kind === 'session') {
    // A pinned row can only move among pinned rows, and vice versa: the server
    // floats the pin group to the top, so letting a drop cross the boundary
    // would snap the row back on the next render.
    const all = sessionsFor(drag.scopeId)
    const pinnedIDs = all.filter((row) => row.pinned).map((row) => row.id)
    const looseIDs = all.filter((row) => !row.pinned).map((row) => row.id)
    const group = drag.item.classList.contains('pinned') ? pinnedIDs : looseIDs
    group.splice(drag.insertAt, 0, drag.id)
    void submitSessionOrder(drag.scopeId, pinnedIDs.concat(looseIDs))
    return
  }
  const ids = worktreeRowIDs(/** @type {HTMLUListElement} */ (drag.list), /** @type {HTMLLIElement} */ (drag.item))
  ids.splice(drag.insertAt, 0, drag.id)
  void submitWorktreeOrder(drag.scopeId, ids)
}

$('tree').addEventListener('pointerdown', (event) => {
  if (readOnly()) return
  const target = eventElement(event)
  const sessionGrip = /** @type {HTMLElement|null} */ (target.closest('[data-session-grip]'))
  if (sessionGrip) {
    const id = sessionGrip.dataset.sessionGrip
    const sess = id ? sessionFor(id) : null
    const item = /** @type {HTMLLIElement|null} */ (sessionGrip.closest('li'))
    const list = /** @type {HTMLUListElement|null} */ (sessionGrip.closest('ul.tree-sessions'))
    if (!id || !sess || !item || !list) return
    event.preventDefault()
    sessionGrip.focus({ preventScroll: true })
    startTreeDrag('session', id, sess.worktreeId, item, list, event.clientY)
    return
  }
  const repoGrip = /** @type {HTMLElement|null} */ (target.closest('[data-repo-grip]'))
  if (repoGrip) {
    const id = repoGrip.dataset.repoGrip
    const item = /** @type {HTMLElement|null} */ (repoGrip.closest('.repo'))
    if (!id || !item) return
    // Keep the browser from starting a text selection or a native drag.
    event.preventDefault()
    repoGrip.focus({ preventScroll: true })
    startTreeDrag('repo', id, id, item, $('tree'), event.clientY)
    return
  }

  const grip = /** @type {HTMLElement|null} */ (target.closest('[data-wt-grip]'))
  const id = grip?.dataset.wtGrip
  const wt = id ? worktreeFor(id) : null
  if (!grip || !id || !wt) return
  const group = state.worktrees.filter((row) => row.repoId === wt.repoId)
  if (group.length === 1) {
    const item = repoBlockFor(wt.repoId)
    if (!item) return
    event.preventDefault()
    grip.focus({ preventScroll: true })
    startTreeDrag('repo', wt.repoId, wt.repoId, item, $('tree'), event.clientY)
    return
  }
  const item = /** @type {HTMLLIElement|null} */ (grip.closest('li.wt-item'))
  const list = /** @type {HTMLUListElement|null} */ (grip.closest('ul.wt-list'))
  if (!item || !list) return
  event.preventDefault()
  grip.focus({ preventScroll: true })
  startTreeDrag('worktree', id, wt.repoId, item, list, event.clientY)
})

window.addEventListener('pointermove', (event) => {
  const drag = treeDrag
  if (!drag) return
  if (!drag.started) {
    if (Math.abs(event.clientY - drag.startY) < 6) return
    drag.started = true
    drag.item.classList.add('dragging')
    document.body.classList.add('wt-dragging')
  }
  event.preventDefault()
  // Keep a long fleet reachable while the pointer is held near an edge.
  const tree = $('tree')
  const box = tree.getBoundingClientRect()
  if (event.clientY < box.top + 28) tree.scrollTop -= 10
  else if (event.clientY > box.bottom - 28) tree.scrollTop += 10

  const rowSelector = drag.kind === 'repo' ? '.repo' : drag.kind === 'worktree' ? 'li.wt-item' : 'li'
  let rows = Array.from(drag.list.children).filter((row) => row !== drag.item && row.matches(rowSelector))
  if (drag.kind === 'session') {
    // Pinned and unpinned rows are separate sort groups; showing a drop mark
    // across the boundary would promise a move the server then undoes.
    const pinned = drag.item.classList.contains('pinned')
    rows = rows.filter((row) => row.classList.contains('pinned') === pinned)
  }
  let insertAt = rows.length
  for (let i = 0; i < rows.length; i++) {
    const rect = rows[i].getBoundingClientRect()
    if (event.clientY < rect.top + rect.height / 2) {
      insertAt = i
      break
    }
  }
  if (insertAt === drag.insertAt) return
  drag.insertAt = insertAt
  clearDropMarks(drag.list)
  if (insertAt < rows.length) rows[insertAt].classList.add('drop-before')
  else if (rows.length) rows[rows.length - 1].classList.add('drop-after')
})

window.addEventListener('pointerup', () => finishTreeDrag(true))
window.addEventListener('pointercancel', () => finishTreeDrag(false))
window.addEventListener('blur', () => finishTreeDrag(false))

// Dragging is not the only way: the grip is a button, so ↑/↓ move one place.
$('tree').addEventListener('keydown', (event) => {
  if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
  const target = eventElement(event)
  const delta = event.key === 'ArrowUp' ? -1 : 1
  const sessionGrip = /** @type {HTMLElement|null} */ (target.closest('[data-session-grip]'))
  if (sessionGrip?.dataset.sessionGrip) {
    event.preventDefault()
    moveSession(sessionGrip.dataset.sessionGrip, delta)
    return
  }
  const repoGrip = /** @type {HTMLElement|null} */ (target.closest('[data-repo-grip]'))
  if (repoGrip) {
    const id = repoGrip.dataset.repoGrip
    if (!id) return
    event.preventDefault()
    moveRepo(id, delta)
    return
  }
  const grip = /** @type {HTMLElement|null} */ (target.closest('[data-wt-grip]'))
  const id = grip?.dataset.wtGrip
  const wt = id ? worktreeFor(id) : null
  if (!grip || !id || !wt) return
  event.preventDefault()
  if (state.worktrees.filter((row) => row.repoId === wt.repoId).length === 1) {
    moveRepo(wt.repoId, delta, id)
    return
  }
  moveWorktree(id, delta)
})

function renderView() {
  const main = $('main')
  const previousView = $('app').dataset.view
  const scrollTop = main.scrollTop
  renderViewNow()
  // Entering the session view reveals a canvas that may have been hidden
  // while its grid was measured. Force one fit/repaint after layout instead of
  // waiting for a manual session switch.
  if ($('app').dataset.view === 'session' && previousView !== 'session') {
    refreshTerminalSoon()
  }
  if ($('app').dataset.view === previousView) main.scrollTop = scrollTop
}

function renderViewNow() {
  const views = ['view-session', 'view-worktree', 'view-empty', 'view-runs', 'view-opencode']
  for (const id of views) $(id).hidden = true
  $('app').dataset.view = 'empty'
  if (selection?.kind !== 'opencode') stopOpenCodePolling()
  if (selection?.kind !== 'opencode') exitChatMode()

  if (selection?.kind === 'opencode') {
    const wt = worktreeFor(openCodeScopeWorktreeId || selection.id || '')
    if (wt) {
      $('app').dataset.view = 'opencode'
      exitZen()
      $('view-opencode').hidden = false
      renderOpenCode()
      applyChatMode(chatMode, false)
      return
    }
    selection = null
  }
  if (selection?.kind === 'runs') {
    $('app').dataset.view = 'runs'
    exitZen()
    $('view-runs').hidden = false
    renderRuns()
    return
  }
  if (selection?.kind === 'session' && currentSession) {
    $('app').dataset.view = 'session'
    $('view-session').hidden = false
    renderSession()
    // Coming back to the view reveals a canvas that was hidden while another
    // view owned the document; nothing writes to it until the PTY speaks, so
    // paint it now.
    refreshTerminal()
    return
  }
  if (selection?.kind === 'worktree') {
    const wt = worktreeFor(selection.id)
    if (wt) {
      $('app').dataset.view = 'worktree'
      exitZen()
      $('view-worktree').hidden = false
      renderWorktree(wt)
      return
    }
    selection = null
  }
  exitZen()
  $('view-empty').hidden = false
  renderEmpty()
}

function renderEmpty() {
  const title = $('empty-title')
  const text = $('empty-text')
  const newSession = buttonEl('btn-empty-session')

  if (!state.repos.length) {
    title.textContent = 'Give an agent a session.'
    text.textContent = 'Add a repo, then start a session in its workspace. Git repos can also use isolated worktrees. The daemon owns the PTY; this browser is a client that can disappear.'
    newSession.disabled = true
    return
  }
  newSession.disabled = false
  if (!state.worktrees.length) {
    title.textContent = 'Start a session.'
    text.textContent = 'Pick a repo. The checkout is the default and shares agent history; choose a new isolated worktree only when you want a separate branch and working tree.'
    return
  }
  title.textContent = 'Pick a checkout or worktree.'
  text.textContent = 'The fleet is in the sidebar. Choose a checkout or worktree to see its sessions, or start a new one. New sessions default to the checkout.'
}

function renderWorktree(wt) {
  const repo = repoFor(wt.repoId)
  const kind = wt.workspace ? 'workspace' : 'worktree'
  $('wt-crumb').textContent = `${repo ? repo.name : 'repo'} · ${kind}`
  $('wt-title').textContent = wtName(wt)
  const remove = buttonEl('btn-wt-remove')
  remove.hidden = false
  remove.textContent = wt.workspace ? 'Remove repo' : 'Remove worktree'
  remove.title = wt.workspace
    ? 'Stops sessions and removes the repo record; files stay on disk'
    : 'Stops sessions and removes the worktree directory; uncommitted changes are refused'

  const chips = $('wt-chips')
  chips.replaceChildren()
  chips.append(pill(wt.workspace ? 'workspace' : (wt.branch || 'branch')))
  const sessions = sessionsFor(wt.id)
  const live = sessions.filter((sess) => sess.live).length
  chips.append(pill(`${sessions.length} session${sessions.length === 1 ? '' : 's'}${live ? ` · ${live} live` : ''}`, live ? 'ok' : ''))
  const containment = wt.containment || 'off'
  chips.append(pill(`containment ${containment}`, containment === 'jail' ? 'ok' : 'warn'))
  if (!wt.workspace && wt.baseRef) chips.append(pill(`base ${wt.baseRef}`, 'info'))

  $('wt-path').textContent = wt.path || '—'
  $('wt-branch').textContent = wt.workspace ? '—' : (wt.branch || '—')
  $('wt-base').textContent = wt.workspace ? '—' : (wt.baseRef || '—')
  $('wt-mode').textContent = containment === 'jail'
    ? 'jail · projected HOME · daemon state masked'
    : 'same-uid (not a sandbox)'

  const list = $('wt-sessions')
  list.replaceChildren()
  if (!sessions.length) {
    list.append(el('li', 'rows-empty', `No sessions yet. Start one to get a PTY in this ${kind}.`))
    return
  }
  for (const sess of sessions) {
    const item = el('li')
    const button = el('button', 'row-btn')
    button.type = 'button'
    button.dataset.session = sess.id
    button.append(sessionActivity(sess))

    const main = el('span', 'row-main')
    main.append(el('span', 'row-title', sess.title || agentLabel(sess)))
    main.append(el('span', 'row-meta', `${agentLabel(sess)} · ${shortId(sess.id)} · ${timeShort(sess.createdAt)}`))
    button.append(main)

    const side = el('span', 'row-side')
    side.append(statusPill(sess))
    button.append(side)
    item.append(button)
    list.append(item)
  }
}

function renderRuns() {
  const list = $('runs-list')
  const card = list.closest('.runs-list-card')
  const scrollTop = card ? card.scrollTop : 0
  list.replaceChildren()
  const runs = [...state.runs].sort((a, b) => String(b.createdAt).localeCompare(String(a.createdAt)))
  if (!runs.length) {
    list.append(el('li', 'rows-empty', 'No runs yet. A run is a goal plus an immutable plan; it is not a session.'))
    currentRunId = ''
    runDetail = null
    renderRunDetail()
    if (card) card.scrollTop = scrollTop
    return
  }
  if (!currentRunId || !runs.some((item) => item.id === currentRunId)) currentRunId = runs[0].id

  for (const run of runs) {
    const wt = worktreeFor(run.worktreeId)
    const item = el('li')
    const button = el('button', 'row-btn run-row')
    button.type = 'button'
    button.dataset.run = run.id
    if (run.id === currentRunId) button.classList.add('on')
    button.append(dot(run.state === 'running' ? 'run' : run.state === 'waiting' || run.state === 'blocked' ? 'block' : 'idle'))
    const main = el('span', 'row-main')
    main.append(el('span', 'row-title', run.goal))
    main.append(el('span', 'row-meta', `${wt ? worktreeLabel(wt) : 'worktree'} · ${timeShort(run.createdAt)}`))
    button.append(main)
    const side = el('span', 'row-side')
    side.append(runStatePill(run))
    button.append(side)
    item.append(button)
    list.append(item)
  }

  const selected = runFor(currentRunId)
  $('runs-title').textContent = selected ? selected.goal : 'Runs'
  const chips = $('runs-chips')
  chips.replaceChildren()
  if (selected) {
    chips.append(runStatePill(selected))
    if (selected.outcome) chips.append(pill(selected.outcome, 'info'))
    chips.append(pill(`plan ${shortId(selected.planHash)}`))
    const wt = worktreeFor(selected.worktreeId)
    if (wt) chips.append(pill(worktreeLabel(wt), 'info'))
  }
  if (!runDetail || runDetail.run.id !== selected?.id || runDetail.run.state !== selected?.state) {
    if (selected) void loadRunDetail(selected.id)
  }
  renderRunDetail()
  if (card) card.scrollTop = scrollTop
}

function worktreeLabel(wt) {
  const repo = repoFor(wt.repoId)
  return `${repo ? repo.name : 'repo'} / ${wtName(wt)}`
}

/** @param {string} id */
async function loadRunDetail(id) {
  const seq = ++runDetailSeq
  runDetailLoading = true
  try {
    const next = /** @type {RunDetail} */ (await api(`/api/v1/runs/${encodeURIComponent(id)}`))
    if (seq !== runDetailSeq || currentRunId !== id) return
    runDetail = next
    renderRunDetail()
  } catch (error) {
    if (seq === runDetailSeq && error.message !== 'unauthorized') {
      const body = $('run-body')
      body.replaceChildren(el('p', 'muted', `Could not load run: ${error.message}`))
    }
  } finally {
    if (seq === runDetailSeq) runDetailLoading = false
  }
}

function renderRunDetail() {
  const body = $('run-body')
  body.replaceChildren()
  const run = runDetail?.run
  if (!run) {
    body.append(el('p', 'muted', 'Select a run to see its plan and steps.'))
    return
  }
  const head = el('div', 'run-head')
  head.append(runStatePill(run))
  if (run.outcome) head.append(pill(run.outcome, 'info'))
  head.append(el('span', 'muted', `plan ${run.planHash.slice(0, 12)}`))
  body.append(head)
  if (run.error) body.append(el('p', 'run-error', run.error))
  if (run.startedAt) body.append(el('p', 'muted run-times', `started ${timeShort(run.startedAt)}${run.endedAt ? ` · ended ${timeShort(run.endedAt)}` : ''}`))

  const actions = el('div', 'run-actions')
  if (run.state === 'draft') {
    const start = el('button', 'btn primary', 'Start run')
    start.type = 'button'
    start.dataset.runStart = run.id
    actions.append(start)
  }
  if (run.state === 'running' || run.state === 'waiting' || run.state === 'blocked') {
    const cancel = el('button', 'btn danger', 'Cancel run')
    cancel.type = 'button'
    cancel.dataset.runCancel = run.id
    actions.append(cancel)
  }
  if (actions.childElementCount) body.append(actions)

  const steps = runDetail?.steps || []
  if (!steps.length) {
    body.append(el('p', 'muted', 'This plan has no steps.'))
    return
  }
  const list = el('ol', 'run-steps')
  for (const step of steps) {
    const item = el('li', 'run-step')
    const headRow = el('div', 'run-step-head')
    headRow.append(el('span', 'run-step-ordinal', `${step.ordinal}.`))
    headRow.append(stepStatePill(step))
    headRow.append(el('span', 'run-step-name', step.name || step.kind))
    headRow.append(el('span', 'muted', step.kind))
    if (step.exitCode !== undefined && step.exitCode !== null) headRow.append(el('span', 'muted', `exit ${step.exitCode}`))
    item.append(headRow)
    if (step.spec?.command?.length) item.append(el('code', 'run-step-spec', step.spec.command.join(' ')))
    if (step.spec?.agent) item.append(el('code', 'run-step-spec', `${step.spec.agent}${step.spec.args?.length ? ' ' + step.spec.args.join(' ') : ''}`))
    if (step.error) item.append(el('p', 'run-error', step.error))
    if (step.output) {
      const pre = el('pre', 'run-output')
      pre.textContent = step.output
      item.append(pre)
    }
    if (step.state === 'waiting' && run.state === 'waiting') {
      const answer = el('div', 'run-actions')
      for (const [label, result, cls] of [['Mark done', 'succeeded', 'primary'], ['Fail step', 'failed', 'danger'], ['Skip', 'skipped', '']]) {
        const button = el('button', `btn ${cls}`.trim(), label)
        button.type = 'button'
        button.dataset.runAnswer = String(step.ordinal)
        button.dataset.runResult = result
        answer.append(button)
      }
      if (step.sessionId) {
        const open = el('button', 'btn', 'Open PTY')
        open.type = 'button'
        open.dataset.runSession = step.sessionId
        answer.append(open)
      }
      item.append(answer)
    }
    list.append(item)
  }
  body.append(list)
}

/* ---------- OpenCode ---------- */

/** @param {string} id */
function openCodeSessionFor(id) {
  return openCodeSessions.find((session) => session.id === id) || null
}

/** @param {number} [ms] */
function timeAgoMs(ms) {
  if (!ms) return ''
  const diff = Date.now() - ms
  if (diff < 60_000) return 'now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`
  return `${Math.floor(diff / 86_400_000)}d`
}

/** @param {OpenCodeSession} session */
function openCodeDirectory(session) {
  const directory = session.location?.directory || ''
  const parts = directory.split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

function openCodeSessionMeta(session) {
  const meta = [
    session.agent,
    session.model?.id,
    openCodeDirectory(session),
    timeAgoMs(session.time?.updated)
  ].filter(Boolean).join(' · ')
  return meta || shortId(session.id)
}

// Structure only. A title changes while a chat streams, and rebuilding the
// list for that replaces the button under the pointer (a click then lands on a
// detached node) and the <option> children under an open Android picker. The
// title and meta lines are refreshed in place instead.
function openCodeListKey() {
  return openCodeSessions.map((session) =>
    `${session.id}:${session.agent || ''}:${openCodeDirectory(session)}`
  ).join('|')
}

function openCodeMessageKey() {
  return openCodeMessages.map((message) => {
    const parts = (message.content || []).map((part) =>
      `${part.type || ''}:${(part.text || '').length}:${part.state?.status || ''}:${(part.name || '')}`
    ).join(',')
    return `${message.id}:${message.time?.completed || 0}:${message.time?.streamed || 0}:${(message.text || '').length}:${message.outcome || ''}:${message.error ? openCodeErrorText(message.error).length : 0}:${parts}`
  }).join('|')
}

/** @param {string} value @param {number} max */
function limitOpenCodeText(value, max) {
  const text = String(value || '')
  return text.length > max ? `${text.slice(0, max)}\n… (${text.length - max} chars hidden)` : text
}

/** @param {any} error */
function openCodeErrorText(error) {
  if (!error) return ''
  if (typeof error === 'string') return limitOpenCodeText(error, 2000)
  if (typeof error.message === 'string') return limitOpenCodeText(error.message, 2000)
  if (typeof error._tag === 'string') return limitOpenCodeText(error._tag, 2000)
  try {
    return limitOpenCodeText(JSON.stringify(error), 2000)
  } catch {
    return 'error'
  }
}

/** @param {string} text */
function preBlock(text) {
  const pre = el('pre', 'oc-code')
  pre.textContent = limitOpenCodeText(text, 20000)
  return pre
}

// Links in agent output are untrusted: only these schemes become anchors.
/** @param {string} url */
function markdownHref(url) {
  const trimmed = String(url || '').trim()
  return /^(https?:\/\/|mailto:)/i.test(trimmed) ? trimmed : ''
}

const MARKDOWN_INLINE = /`([^`\n]+)`|\*\*([^*\n]+)\*\*|\*([^*\n]+)\*|\[([^\]\n]*)\]\(([^)\s]+)\)/
const MARKDOWN_BLOCK = /^ {0,3}(#{1,6}\s|```|~~~|>|([-*+]|\d+[.)])\s+|([-*_])(?:\s*\3){2,}\s*$)/

/**
 * Append `text` to `out`, turning inline code, bold, italic and links into
 * nodes. Every node is built with createElement/textContent: a message can
 * never inject markup, and an unsupported scheme stays literal text.
 * @param {string} text
 * @param {Node} out
 */
function appendMarkdownInline(text, out) {
  let rest = text
  while (rest) {
    const match = MARKDOWN_INLINE.exec(rest)
    if (!match) break
    if (match.index > 0) out.appendChild(document.createTextNode(rest.slice(0, match.index)))
    rest = rest.slice(match.index + match[0].length)
    if (match[1] !== undefined) {
      out.appendChild(el('code', 'oc-inline', match[1]))
    } else if (match[2] !== undefined) {
      const strong = document.createElement('strong')
      appendMarkdownInline(match[2], strong)
      out.appendChild(strong)
    } else if (match[3] !== undefined) {
      const em = document.createElement('em')
      appendMarkdownInline(match[3], em)
      out.appendChild(em)
    } else if (match[4] !== undefined) {
      const href = markdownHref(match[5] || '')
      if (!href) {
        out.appendChild(document.createTextNode(match[0]))
      } else {
        const link = document.createElement('a')
        link.href = href
        link.textContent = match[4] || href
        link.target = '_blank'
        link.rel = 'noopener noreferrer'
        out.appendChild(link)
      }
    }
  }
  if (rest) out.appendChild(document.createTextNode(rest))
}

/** @param {string} text @returns {HTMLElement} */
function markdownParagraph(text) {
  const paragraph = document.createElement('p')
  appendMarkdownInline(text, paragraph)
  return paragraph
}

/** @param {string} line @param {'th'|'td'} tag @returns {HTMLTableCellElement[]} */
function markdownCells(line, tag) {
  return line.replace(/^\s*\|/, '').replace(/\|\s*$/, '').split('|').map((cell) => {
    const node = document.createElement(tag)
    if (tag === 'th') node.classList.add('oc-th')
    appendMarkdownInline(cell.trim(), node)
    return node
  })
}

const MARKDOWN_FENCE = /^ {0,3}(```+|~~~+)\s*([A-Za-z0-9_+.-]{0,24})\s*$/
const MARKDOWN_HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/
const MARKDOWN_RULE = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/
const MARKDOWN_ITEM = /^ {0,3}([-*+]|\d+[.)])\s+(.*)$/
const MARKDOWN_TABLE_RULE = /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/
const MARKDOWN_QUOTE = /^ {0,3}>\s?/

/**
 * Render a markdown subset as a block container: fenced code, headings, rules,
 * quotes, flat lists, simple pipe tables and paragraphs. Single newlines inside
 * a paragraph are hard breaks, because agent output is usually line-per-line.
 * @param {string} text
 * @returns {HTMLElement}
 */
function renderMarkdown(text) {
  const box = el('div', 'oc-text oc-md')
  appendMarkdownBlocks(text, box)
  return box
}

/** @param {string} text @param {HTMLElement} box */
function appendMarkdownBlocks(text, box) {
  const lines = String(text || '').replace(/\r\n?/g, '\n').split('\n')
  let index = 0
  while (index < lines.length) {
    const line = lines[index]
    if (!line.trim()) {
      index++
      continue
    }

    const fence = MARKDOWN_FENCE.exec(line)
    if (fence) {
      const marker = fence[1][0]
      const close = new RegExp('^ {0,3}' + (marker === '`' ? '```' : '~~~') + '+\\s*$')
      const body = []
      index++
      while (index < lines.length && !close.test(lines[index])) {
        body.push(lines[index])
        index++
      }
      index++
      box.appendChild(preBlock(body.join('\n')))
      continue
    }

    const heading = MARKDOWN_HEADING.exec(line)
    if (heading) {
      const node = document.createElement(/** @type {'h1'} */ ('h' + heading[1].length))
      appendMarkdownInline(heading[2], node)
      box.appendChild(node)
      index++
      continue
    }

    if (MARKDOWN_RULE.test(line)) {
      box.appendChild(document.createElement('hr'))
      index++
      continue
    }

    if (MARKDOWN_QUOTE.test(line)) {
      const body = []
      while (index < lines.length && MARKDOWN_QUOTE.test(lines[index])) {
        body.push(lines[index].replace(MARKDOWN_QUOTE, ''))
        index++
      }
      const quote = document.createElement('blockquote')
      appendMarkdownBlocks(body.join('\n'), quote)
      box.appendChild(quote)
      continue
    }

    // A pipe table needs its separator row; a stray pipe stays in a paragraph.
    if (line.includes('|') && index + 1 < lines.length && MARKDOWN_TABLE_RULE.test(lines[index + 1])) {
      const table = document.createElement('table')
      const head = document.createElement('thead')
      const headRow = document.createElement('tr')
      for (const cell of markdownCells(line, 'th')) {
        headRow.appendChild(cell)
      }
      head.appendChild(headRow)
      table.appendChild(head)
      index += 2
      const body = document.createElement('tbody')
      while (index < lines.length && lines[index].includes('|') && lines[index].trim()) {
        const row = document.createElement('tr')
        for (const cell of markdownCells(lines[index], 'td')) row.appendChild(cell)
        body.appendChild(row)
        index++
      }
      table.appendChild(body)
      box.appendChild(table)
      continue
    }

    const item = MARKDOWN_ITEM.exec(line)
    if (item) {
      const ordered = /\d/.test(item[1])
      const list = document.createElement(ordered ? 'ol' : 'ul')
      while (index < lines.length) {
        const next = MARKDOWN_ITEM.exec(lines[index])
        if (!next) break
        const entry = document.createElement('li')
        appendMarkdownInline(next[2], entry)
        list.appendChild(entry)
        index++
      }
      box.appendChild(list)
      continue
    }

    const paragraph = document.createElement('p')
    while (index < lines.length && lines[index].trim() && !MARKDOWN_BLOCK.test(lines[index])) {
      if (paragraph.childNodes.length) paragraph.appendChild(document.createElement('br'))
      appendMarkdownInline(lines[index], paragraph)
      index++
    }
    if (!paragraph.childNodes.length) {
      // The line starts a block the parser did not claim; keep it as text.
      appendMarkdownInline(lines[index], paragraph)
      index++
    }
    box.appendChild(paragraph)
  }
}

/**
 * Render OpenCode's Markdown-ish text. Agent output is untrusted, so this builds
 * DOM nodes and never parses HTML from the message.
 * @param {string} text
 */
function renderOpenCodeText(text) {
  const fragment = document.createDocumentFragment()
  const body = limitOpenCodeText(text || '', 40000)
  if (!body.trim()) {
    fragment.appendChild(el('div', 'oc-text', '…'))
    return fragment
  }
  fragment.appendChild(renderMarkdown(body))
  return fragment
}

/** @param {OpenCodePart} part */
function renderOpenCodePart(part) {
  if (part.type === 'text') return renderOpenCodeText(part.text || '')
  if (part.type === 'reasoning') {
    const details = el('details', 'oc-reasoning')
    details.append(el('summary', '', 'Reasoning'))
    const body = el('div', 'oc-reasoning-body')
    let filled = false
    const fill = () => {
      if (filled) return
      filled = true
      body.append(renderOpenCodeText(limitOpenCodeText(part.text || '', 20000)))
    }
    details.addEventListener('toggle', () => { if (details.open) fill() })
    details.append(body)
    return details
  }
  if (part.type === 'tool') {
    const state = part.state || {}
    const details = el('details', 'oc-tool')
    const summary = el('summary')
    summary.append(el('span', 'oc-tool-name', part.name || 'tool'))
    summary.append(el('span', `oc-tool-status ${state.status || ''}`.trim(), state.status || ''))
    details.append(summary)
    // A page of tool output can be megabytes of collapsed <details>. Build the
    // body only when it is opened; otherwise the initial paint blocks input.
    const body = el('div', 'oc-tool-body')
    let filled = false
    const fill = () => {
      if (filled) return
      filled = true
      if (state.input !== undefined && state.input !== null) {
        body.append(el('div', 'oc-role', 'input'))
        body.append(preBlock(typeof state.input === 'string' ? state.input : JSON.stringify(state.input, null, 2)))
      }
      for (const item of state.content || []) {
        if (!item?.text) continue
        body.append(el('div', 'oc-role', item.type || 'output'))
        body.append(preBlock(item.text))
      }
      if (state.error) body.append(el('p', 'oc-error', openCodeErrorText(state.error)))
    }
    details.addEventListener('toggle', () => { if (details.open) fill() })
    details.append(body)
    return details
  }
  return renderOpenCodeText(part.text || '')
}

/** @param {OpenCodeMessage} message */
function renderOpenCodeMessage(message) {
  if (message.type === 'idle') {
    const node = el('div', 'oc-idle', message.outcome ? `turn ${message.outcome}` : 'turn complete')
    if (message.error) node.append(el('div', 'oc-error', openCodeErrorText(message.error)))
    return node
  }
  const wrap = el('div', `oc-msg ${message.type}`)
  const bubble = el('div', 'oc-bubble')
  bubble.append(el('div', 'oc-role', message.type === 'user' ? 'you' : message.type))
  if (message.type === 'user' || message.type === 'system' || message.type === 'synthetic') {
    bubble.append(renderOpenCodeText(message.text || ''))
  } else {
    const content = message.content || []
    if (!content.length && message.text) bubble.append(renderOpenCodeText(message.text))
    for (const part of content) bubble.append(renderOpenCodePart(part))
    if (message.error) bubble.append(el('p', 'oc-error', openCodeErrorText(message.error)))
  }
  wrap.append(bubble)
  return wrap
}

/**
 * Log a control's device-pixel box when it moves. A screenshot probe cannot find
 * a control by role, and hard-coded pixels rot with every layout change, so the
 * page says where its own picker, list and buttons are and the probe reads it.
 *
 * The box is scaled by devicePixelRatio because `adb shell input tap` speaks
 * device pixels while getBoundingClientRect speaks CSS pixels.
 * @param {string} name
 * @param {Element|null} node
 */
function debugOpenCodeBox(name, node) {
  if (!DEBUG || !node) return
  const rect = node.getBoundingClientRect()
  if (!rect.width || !rect.height) return
  const dpr = window.devicePixelRatio || 1
  const box = [rect.left, rect.top, rect.width, rect.height]
    .map((value) => Math.round(value * dpr))
    .join(',')
  if (openCodeBoxes[name] === box) return
  openCodeBoxes[name] = box
  debugLog('oc', `${name} box ${box}`)
}

/**
 * Re-read every control box a phone probe taps. Chat mode, the soft keyboard and
 * view switches move these without re-rendering the panel, so they are reported
 * again after the frame settles instead of only when the state did.
 */
function reportOpenCodeBoxes() {
  if (!DEBUG) return
  debugOpenCodeBox('new', $('btn-oc-new'))
  debugOpenCodeBox('prompt', $('oc-prompt'))
  debugOpenCodeBox('send', $('btn-oc-send'))
  debugOpenCodeBox('stop', $('btn-oc-interrupt'))
  debugOpenCodeBox('picker', selectEl('oc-session-select'))
  debugOpenCodeBox('messages', $('oc-messages'))
  debugOpenCodeBox('latest', $('btn-oc-latest'))
}

function renderOpenCodeSessions() {
  const list = $('oc-sessions')
  const select = selectEl('oc-session-select')
  debugOpenCodeBox('picker', select)
  if (!state.opencode) {
    openCodeListSignature = ''
    list.replaceChildren(el('p', 'rows-empty', 'OpenCode is not configured.'))
    select.replaceChildren()
    const option = el('option', '', 'OpenCode is not configured')
    option.value = ''
    select.append(option)
    return
  }
  // The mobile session switcher is a native <select>, and its popup lives in
  // the browser UI. Rebuilding the options, or reassigning the value, while it
  // is open orphans that popup: the row tap fires `change` and the app switches,
  // but the popup stays on screen and later taps inside it do nothing. A select
  // is focused for as long as its popup is up, so defer both until it closes.
  const pickerOpen = select === document.activeElement
  const key = openCodeListKey()
  if (key !== openCodeListSignature && !pickerOpen) {
    openCodeListSignature = key
    debugLog('oc', `list rebuild sessions=${openCodeSessions.length}`)
    const scrollTop = list.scrollTop
    list.replaceChildren()
    select.replaceChildren()
    if (!openCodeSessions.length) {
      list.append(el('p', 'rows-empty', state.opencode ? 'No chats in this worktree yet.' : 'OpenCode is not configured.'))
      const option = el('option', '', state.opencode ? 'No chats' : 'OpenCode is not configured')
      option.value = ''
      select.append(option)
    } else {
      for (const session of openCodeSessions) {
        const button = el('button', 'oc-session')
        button.type = 'button'
        button.dataset.ocSession = session.id
        button.title = session.title || session.id
        button.append(el('span', 'oc-session-title', session.title || 'Untitled chat'))
        button.append(el('span', 'oc-session-meta', openCodeSessionMeta(session)))
        list.append(button)

        const option = el('option', '', session.title || 'Untitled chat')
        option.value = session.id
        select.append(option)
      }
    }
    list.scrollTop = scrollTop
  }

  const meta = new Map(openCodeSessions.map((session) => [session.id, openCodeSessionMeta(session)]))
  const titles = new Map(openCodeSessions.map((session) => [session.id, session.title || 'Untitled chat']))
  const options = new Map(Array.from(select.options).map((option) => [option.value, option]))
  for (const button of list.querySelectorAll('[data-oc-session]')) {
    const node = /** @type {HTMLElement} */ (button)
    node.classList.toggle('on', node.dataset.ocSession === openCodeSessionId)
    const id = node.dataset.ocSession || ''
    const line = node.querySelector('.oc-session-meta')
    if (line && meta.has(id) && line.textContent !== meta.get(id)) line.textContent = meta.get(id)
    const title = node.querySelector('.oc-session-title')
    if (title && titles.has(id) && title.textContent !== titles.get(id)) title.textContent = titles.get(id)
    const option = options.get(id)
    if (option && titles.has(id) && option.textContent !== titles.get(id)) option.textContent = titles.get(id)
  }
  if (!pickerOpen) select.value = openCodeSessionId
}

function renderOpenCodeMessages() {
  const box = $('oc-messages')
  const title = $('oc-chat-title')
  debugOpenCodeBox('messages', box)
  const session = openCodeSessionFor(openCodeSessionId)
  if (!state.opencode) {
    title.textContent = 'OpenCode is not configured'
    box.replaceChildren(el('div', 'oc-empty', 'OpenCode service credentials are not available. Set opencodePasswordFile in the DarkFactory config, or start OpenCode once.'))
    $('btn-oc-latest').hidden = true
    $('btn-oc-interrupt').hidden = true
    $('btn-oc-older').hidden = true
    openCodeMessageSignature = ''
    return
  }
  if (!session) {
    title.textContent = 'Select a chat'
    box.replaceChildren(el('div', 'oc-empty', openCodeSessions.length
      ? 'Pick an OpenCode chat on the left, or start a new one.'
      : 'No OpenCode chats yet. Start one to talk to OpenCode without the TUI.'))
    $('btn-oc-latest').hidden = true
    $('btn-oc-interrupt').hidden = true
    $('btn-oc-older').hidden = true
    openCodeMessageSignature = ''
    return
  }
  title.textContent = session.title || 'Untitled chat'
  $('btn-oc-interrupt').hidden = false

  buttonEl('btn-oc-older').hidden = !openCodeOlderCursor || openCodeOlderLoading
  const key = openCodeMessageKey()
  if (key === openCodeMessageSignature) {
    updateOpenCodeLatest()
    return
  }
  openCodeMessageSignature = key
  const stick = openCodeStick || openCodeNearBottom()
  const scrollTop = box.scrollTop
  box.replaceChildren()
  if (!openCodeMessages.length) {
    box.append(el('div', 'oc-empty', 'No messages yet. Send one below.'))
  } else {
    for (const message of openCodeMessages) box.append(renderOpenCodeMessage(message))
  }
  if (stick) scrollOpenCodeLatest(false)
  else box.scrollTop = scrollTop
  updateOpenCodeLatest()
}

function renderOpenCodePermissions() {
  const box = $('oc-permissions')
  box.replaceChildren()
  if (!openCodePermissions.length) {
    box.hidden = true
    return
  }
  box.hidden = false
  for (const request of openCodePermissions) {
    const card = el('div', 'oc-permission')
    const head = el('div', 'oc-permission-head')
    head.append(el('span', 'oc-permission-title', 'Permission requested'))
    head.append(el('span', 'oc-permission-action', limitOpenCodeText(request.action || 'action', 200)))
    if (request.source?.type) head.append(el('span', 'oc-permission-source', request.source.type))
    card.append(head)
    if (request.message) card.append(el('p', 'oc-permission-message', limitOpenCodeText(request.message, 4000)))
    const resources = Array.isArray(request.resources) ? request.resources : []
    if (resources.length) {
      const list = el('ul', 'oc-permission-resources')
      for (const resource of resources.slice(0, 20)) {
        list.append(el('li', '', limitOpenCodeText(resource, 2000)))
      }
      card.append(list)
    }
    const actions = el('div', 'oc-permission-actions')
    const buttons = [
      ['once', 'Allow once'],
      ['always', 'Always allow'],
      ['reject', 'Reject']
    ]
    for (const [decision, label] of buttons) {
      const button = el('button', `btn ${decision === 'reject' ? 'danger' : decision === 'always' ? 'ghost' : 'primary'}`, label)
      button.type = 'button'
      button.dataset.ocPermission = request.id
      button.dataset.ocDecision = decision
      button.disabled = readOnly() || openCodePermissionReplying === request.id
      actions.append(button)
    }
    card.append(actions)
    box.append(card)
    debugOpenCodeBox('allow', card.querySelector('[data-oc-decision="once"]'))
  }
}

/* ---------- slash commands ---------- */

// Commands are per location: OpenCode loads project commands from the chat's
// directory. The daemon accepts a location only when it is one of its own
// worktrees, so the browser cannot point the list at another checkout.
async function ensureOpenCodeCommands() {
  if (!state.opencode || openCodeCommandsLoading) return
  const directory = openCodeSessionFor(openCodeSessionId)?.location?.directory || ''
  if (openCodeCommandsLoaded && directory === openCodeCommandLocation) return
  openCodeCommandsLoading = true
  try {
    const query = directory ? `?location=${encodeURIComponent(directory)}` : ''
    const data = await api(`/api/v1/opencode/commands${query}`)
    const list = Array.isArray(data?.data) ? data.data : []
    openCodeCommands = list
      .filter((item) => item && typeof item.name === 'string' && item.name)
      .map((item) => ({ name: item.name, description: String(item.description || '') }))
    openCodeCommandLocation = directory
    openCodeCommandsLoaded = true
    debugLog('oc', `commands ${openCodeCommands.length} for ${shortId(directory) || 'default'}`)
  } catch (error) {
    debugLog('error', `commands failed: ${error.message}`)
  } finally {
    openCodeCommandsLoading = false
  }
}

// The completer only triggers on a bare `/word` in the composer, so a slash in
// ordinary prose ("and/or") never opens it.
function openCodeCommandQuery() {
  const match = /^\/([A-Za-z0-9_.:-]*)$/.exec(inputEl('oc-prompt').value)
  return match ? match[1] : null
}

function closeOpenCodeCommands() {
  const box = $('oc-commands')
  if (!box.hidden) box.hidden = true
  openCodeCommandIndex = 0
}

function highlightOpenCodeCommand() {
  const items = Array.from($('oc-commands').querySelectorAll('[data-command]'))
  items.forEach((item, index) => {
    const on = index === openCodeCommandIndex
    item.classList.toggle('on', on)
    item.setAttribute('aria-selected', on ? 'true' : 'false')
    if (on) item.scrollIntoView({ block: 'nearest' })
  })
}

function renderOpenCodeCommandList() {
  const query = openCodeCommandQuery()
  const box = $('oc-commands')
  if (query === null) {
    closeOpenCodeCommands()
    return
  }
  const matches = openCodeCommands.filter((command) => command.name.startsWith(query)).slice(0, 12)
  if (!matches.length) {
    closeOpenCodeCommands()
    return
  }
  if (openCodeCommandIndex >= matches.length) openCodeCommandIndex = 0
  box.replaceChildren()
  matches.forEach((command, index) => {
    const item = el('button', 'oc-command')
    item.type = 'button'
    item.dataset.command = command.name
    item.setAttribute('role', 'option')
    item.setAttribute('aria-selected', index === openCodeCommandIndex ? 'true' : 'false')
    item.append(el('span', 'oc-command-name', `/${command.name}`))
    if (command.description) item.append(el('span', 'oc-command-desc', command.description))
    box.append(item)
  })
  box.hidden = false
  highlightOpenCodeCommand()
}

function updateOpenCodeCommands() {
  if (openCodeCommandQuery() === null) {
    closeOpenCodeCommands()
    return
  }
  if (!openCodeCommands.length) {
    // First `/` in a tab: fetch, then render without waiting for another key.
    void ensureOpenCodeCommands().then(() => {
      if (openCodeCommandQuery() !== null) renderOpenCodeCommandList()
    })
    return
  }
  renderOpenCodeCommandList()
}

/** @param {string} name */
function chooseOpenCodeCommand(name) {
  const input = inputEl('oc-prompt')
  input.value = `/${name} `
  closeOpenCodeCommands()
  input.focus()
  input.style.height = 'auto'
  input.style.height = `${Math.min(input.scrollHeight, 160)}px`
}

/** @param {KeyboardEvent} event @returns {boolean} true when the key was used */
function handleOpenCodeCommandKeys(event) {
  const box = $('oc-commands')
  if (box.hidden) return false
  const items = Array.from(box.querySelectorAll('[data-command]'))
  if (!items.length) return false
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const step = event.key === 'ArrowDown' ? 1 : -1
    openCodeCommandIndex = (openCodeCommandIndex + step + items.length) % items.length
    highlightOpenCodeCommand()
    return true
  }
  if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
    const item = /** @type {HTMLElement|undefined} */ (items[openCodeCommandIndex])
    if (!item?.dataset.command) return false
    event.preventDefault()
    chooseOpenCodeCommand(item.dataset.command)
    return true
  }
  if (event.key === 'Escape') {
    event.preventDefault()
    closeOpenCodeCommands()
    return true
  }
  return false
}

// A leading /name is a command only when OpenCode registered that name; anything
// else stays a prompt, so "/10 in the log" is still a message.
/** @param {string} text */
function parseOpenCodeCommand(text) {
  const match = /^\/([A-Za-z0-9_.:-]+)(?:\s+([\s\S]*))?$/.exec(text)
  if (!match) return null
  const name = match[1]
  if (!openCodeCommands.some((command) => command.name === name)) return null
  return { name, text: (match[2] || '').trim() }
}

/* ---------- OpenCode panel ---------- */

function renderOpenCodeChips() {
  const chips = $('oc-chips')
  chips.replaceChildren()
  const wt = worktreeFor(openCodeScopeWorktreeId)
  if (wt) {
    chips.append(pill(wt.workspace ? 'workspace' : (wt.branch || 'branch'), 'info'))
    if (wt.path) chips.append(pill(wt.path, ''))
  }
  if (state.opencode) {
    chips.append(pill(`${openCodeSessions.length} chat${openCodeSessions.length === 1 ? '' : 's'}`, openCodeSessions.length ? 'ok' : ''))
    const session = openCodeSessionFor(openCodeSessionId)
    if (session?.agent) chips.append(pill(session.agent, 'info'))
    if (session?.model?.id) chips.append(pill(session.model.id, 'info'))
  } else {
    chips.append(pill('not configured', 'warn'))
  }
  if (readOnly()) chips.append(pill('read-only', 'warn'))
}

function renderOpenCode() {
  const wt = worktreeFor(openCodeScopeWorktreeId)
  const repo = wt ? repoFor(wt.repoId) : null
  $('oc-title').textContent = wt ? wtName(wt) : 'Chat'
  $('oc-crumb').textContent = `${repo ? repo.name : 'repo'} · chat`
  buttonEl('btn-oc-worktree').hidden = !wt
  renderOpenCodeChips()
  inputEl('oc-prompt').disabled = readOnly()
  buttonEl('btn-oc-send').disabled = readOnly() || openCodeSending
  buttonEl('btn-oc-interrupt').disabled = readOnly() || openCodeInterrupting
  buttonEl('btn-oc-new').disabled = readOnly()
  buttonEl('btn-oc-new-side').disabled = readOnly()
  reportOpenCodeBoxes()
  renderOpenCodeSessions()
  renderOpenCodeMessages()
  renderOpenCodePermissions()
  void ensureOpenCodeCommands()
}

function openCodeNearBottom() {
  const box = $('oc-messages')
  return box.scrollHeight - box.scrollTop - box.clientHeight < 90
}

/** @param {boolean} [smooth] */
function scrollOpenCodeLatest(smooth = false) {
  const box = $('oc-messages')
  const from = Math.round(box.scrollTop)
  openCodeStick = true
  if (smooth) {
    box.scrollTo({ top: box.scrollHeight, behavior: 'smooth' })
  } else {
    // iOS defeats a single programmatic jump three ways: a flick that is still
    // decelerating swallows the scroll, one assignment clamps against a list
    // that grew in the same frame, and the scroll event fires mid-flight so the
    // listener would recompute `openCodeStick` from a partial position. Assign
    // scrollTop directly, re-apply after layout, and hold the stick flag while
    // the jump is in flight.
    openCodeJumping = true
    box.scrollTop = box.scrollHeight
    window.requestAnimationFrame(() => {
      box.scrollTop = box.scrollHeight
      openCodeJumping = false
      openCodeStick = true
      updateOpenCodeLatest()
    })
  }
  updateOpenCodeLatest()
  window.requestAnimationFrame(() => updateOpenCodeLatest())
  debugLog('oc', `latest jump from=${from} to=${Math.round(box.scrollTop)} max=${Math.round(box.scrollHeight - box.clientHeight)}`)
}

function updateOpenCodeLatest() {
  const near = openCodeNearBottom()
  const button = $('btn-oc-latest')
  if (button.hidden !== near) {
    const box = $('oc-messages')
    debugLog('oc', `latest ${near ? 'hide' : 'show'} top=${Math.round(box.scrollTop)} max=${Math.round(box.scrollHeight - box.clientHeight)}`)
  }
  button.hidden = near
  if (!near) debugOpenCodeBox('latest', button)
}

function stopOpenCodePolling() {
  if (openCodePollTimer) {
    window.clearTimeout(openCodePollTimer)
    openCodePollTimer = 0
  }
  if (openCodeEventTimer) {
    window.clearTimeout(openCodeEventTimer)
    openCodeEventTimer = 0
  }
  if (openCodeEventRetry) {
    window.clearTimeout(openCodeEventRetry)
    openCodeEventRetry = 0
  }
  if (openCodeEventAbort) {
    openCodeEventAbort.abort()
    openCodeEventAbort = null
  }
}

function startOpenCodePolling() {
  stopOpenCodePolling()
  // The event stream is the low-latency path. A slow poll remains as a
  // fallback because OpenCode's stream is explicitly volatile and may drop.
  void startOpenCodeEvents()
  const tick = async () => {
    openCodePollTimer = 0
    if (selection?.kind !== 'opencode') return
    if (!document.hidden) await refreshOpenCode()
    if (selection?.kind === 'opencode' && state.opencode) {
      openCodePollTimer = window.setTimeout(() => { void tick() }, 15000)
    }
  }
  openCodePollTimer = window.setTimeout(() => { void tick() }, 0)
}

function scheduleOpenCodeRefresh() {
  if (openCodeEventTimer) return
  openCodeEventTimer = window.setTimeout(() => {
    openCodeEventTimer = 0
    if (selection?.kind === 'opencode' && !document.hidden) void refreshOpenCode()
  }, 800)
}

async function startOpenCodeEvents() {
  if (!state.opencode || selection?.kind !== 'opencode' || document.hidden) return
  if (openCodeEventAbort) return
  const controller = new AbortController()
  openCodeEventAbort = controller
  try {
    /** @type {Record<string, string>} */
    const headers = {}
    if (token) headers.Authorization = `Bearer ${token}`
    const response = await fetch('/api/v1/opencode/events', {
      headers,
      credentials: 'same-origin',
      signal: controller.signal,
      cache: 'no-store'
    })
    if (response.status === 401) {
      lostAuth()
      return
    }
    if (!response.ok || !response.body) {
      const refusal = await refusalFrom(response)
      throw new Error(refusalMessage(refusal, response.statusText))
    }
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let index = buffer.indexOf('\n')
      while (index >= 0) {
        const line = buffer.slice(0, index).replace(/\r$/, '')
        buffer = buffer.slice(index + 1)
        if (line.startsWith('data:')) scheduleOpenCodeRefresh()
        index = buffer.indexOf('\n')
      }
    }
  } catch (error) {
    if (error.name !== 'AbortError') debugLog('error', `opencode events: ${error.message}`)
  } finally {
    if (openCodeEventAbort === controller) openCodeEventAbort = null
  }
  if (!controller.signal.aborted && selection?.kind === 'opencode' && state.opencode && !document.hidden) {
    openCodeEventRetry = window.setTimeout(() => {
      openCodeEventRetry = 0
      void startOpenCodeEvents()
    }, 3000)
  }
}

/** @param {{sessions?: boolean}} [options] */
async function refreshOpenCode(options = {}) {
  if (!state.opencode) {
    stopOpenCodePolling()
    renderOpenCode()
    return
  }
  openCodePollTick++
  const wantSessions = options.sessions || !openCodeSessions.length || openCodePollTick % 2 === 0
  if (wantSessions) await loadOpenCodeSessions()
  if (openCodeSessionId) {
    // A chat page can be megabytes; an event for a streaming turn must not
    // re-download it on every line. Selection loads immediately.
    if (Date.now() - openCodeMessagesFetchedAt >= OPENCODE_MESSAGE_MIN_INTERVAL) {
      await loadOpenCodeMessages()
    }
    await loadOpenCodePermissions()
  }
}

async function loadOpenCodeSessions() {
  const seq = ++openCodeSessionSeq
  try {
    // One page is all the panel needs for a personal daemon, and the scope
    // filter below runs on this page: a worktree with more than 100 chats
    // would hide its oldest one, which is why the empty state says "yet".
    const query = new URLSearchParams({ limit: '100', order: 'desc' })
    if (openCodeSearch) query.set('search', openCodeSearch)
    const next = await api(`/api/v1/opencode/sessions?${query}`)
    if (seq !== openCodeSessionSeq) return
    const all = Array.isArray(next?.data) ? next.data : []
    // OpenCode reports a session's directory; the panel is scoped to one
    // worktree, so a chat from anywhere else is out of scope even when the
    // search matched it.
    openCodeSessions = openCodeScopeDirectory
      ? all.filter((session) => (session.location?.directory || '') === openCodeScopeDirectory)
      : all
    if (!openCodeSessions.some((session) => session.id === openCodeSessionId)) {
      openCodeSessionId = openCodeSessions[0]?.id || ''
      openCodeMessages = []
      openCodePermissions = []
      openCodeMessageSignature = OPENCODE_MESSAGE_STALE
      openCodeOlderCursor = ''
      openCodeLoadedOlder = false
      openCodeStick = true
    }
    renderOpenCodeChips()
    renderOpenCodeSessions()
    renderOpenCodeMessages()
    renderOpenCodePermissions()
  } catch (error) {
    if (error.message === 'unauthorized') return
    debugLog('error', `opencode sessions: ${error.message}`)
    openCodeListSignature = 'error'
    if (!openCodeSessions.length) {
      $('oc-chat-title').textContent = 'OpenCode unavailable'
      $('oc-messages').replaceChildren(el('div', 'oc-empty', `Could not load OpenCode chats: ${error.message}`))
    } else {
      toast(`OpenCode: ${error.message}`, true)
    }
  }
}

async function loadOpenCodeMessages() {
  const id = openCodeSessionId
  if (!id) {
    openCodeMessages = []
    openCodeMessageSignature = ''
    openCodeOlderCursor = ''
    openCodeLoadedOlder = false
    renderOpenCodeMessages()
    return
  }
  // A streaming turn emits many events. The page is up to a few megabytes, so
  // do not start another fetch while the previous one is still the current
  // page; the event/poll path checks the interval too.
  openCodeMessagesFetchedAt = Date.now()
  const seq = ++openCodeMessageSeq
  try {
    const next = await api(`/api/v1/opencode/sessions/${encodeURIComponent(id)}/messages?limit=${OPENCODE_MESSAGE_PAGE}&order=desc`)
    if (seq !== openCodeMessageSeq || id !== openCodeSessionId) return
    const newest = Array.isArray(next?.data) ? next.data.slice().reverse() : []
    if (openCodeLoadedOlder && openCodeMessages.length) {
      // A refresh must not throw away pages the user explicitly loaded.
      const fresh = new Map(newest.map((message) => [message.id, message]))
      const seen = new Set(openCodeMessages.map((message) => message.id))
      openCodeMessages = openCodeMessages.map((message) => fresh.get(message.id) || message)
      for (const message of newest) {
        if (!seen.has(message.id)) openCodeMessages.push(message)
      }
    } else {
      openCodeMessages = newest
      openCodeOlderCursor = typeof next?.cursor?.next === 'string' ? next.cursor.next : ''
    }
    renderOpenCodeMessages()
  } catch (error) {
    if (error.message === 'unauthorized') return
    debugLog('error', `opencode messages: ${error.message}`)
    openCodeMessageSignature = 'error'
    if (!openCodeMessages.length) {
      $('oc-messages').replaceChildren(el('div', 'oc-empty', `Could not load messages: ${error.message}`))
    } else {
      toast(`OpenCode: ${error.message}`, true)
    }
  }
}

async function loadOlderOpenCodeMessages() {
  const id = openCodeSessionId
  const cursor = openCodeOlderCursor
  if (!id || !cursor || openCodeOlderLoading) return
  openCodeOlderLoading = true
  buttonEl('btn-oc-older').hidden = true
  const box = $('oc-messages')
  const previousHeight = box.scrollHeight
  const previousTop = box.scrollTop
  try {
    const query = new URLSearchParams({ limit: String(OPENCODE_MESSAGE_PAGE), cursor })
    const next = await api(`/api/v1/opencode/sessions/${encodeURIComponent(id)}/messages?${query}`)
    if (id !== openCodeSessionId) return
    const older = Array.isArray(next?.data) ? next.data.slice().reverse() : []
    openCodeMessages = older.concat(openCodeMessages)
    openCodeOlderCursor = typeof next?.cursor?.next === 'string' ? next.cursor.next : ''
    openCodeLoadedOlder = true
    openCodeMessageSignature = ''
    renderOpenCodeMessages()
    box.scrollTop = previousTop + (box.scrollHeight - previousHeight)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Could not load older messages: ${error.message}`, true)
  } finally {
    openCodeOlderLoading = false
    buttonEl('btn-oc-older').hidden = !openCodeOlderCursor
  }
}

async function loadOpenCodePermissions() {
  const id = openCodeSessionId
  if (!id) {
    openCodePermissions = []
    renderOpenCodePermissions()
    return
  }
  const seq = ++openCodePermissionSeq
  try {
    const next = await api(`/api/v1/opencode/sessions/${encodeURIComponent(id)}/permissions`)
    if (seq !== openCodePermissionSeq || id !== openCodeSessionId) return
    openCodePermissions = Array.isArray(next?.data) ? next.data : []
    renderOpenCodePermissions()
  } catch (error) {
    if (error.message === 'unauthorized') return
    debugLog('error', `opencode permissions: ${error.message}`)
  }
}

async function replyOpenCodePermission(requestID, decision) {
  if (!openCodeSessionId || openCodePermissionReplying) return
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  openCodePermissionReplying = requestID
  renderOpenCodePermissions()
  try {
    await api(`/api/v1/opencode/sessions/${encodeURIComponent(openCodeSessionId)}/permissions/${encodeURIComponent(requestID)}/reply`, {
      method: 'POST',
      body: { decision }
    })
    toast(decision === 'reject' ? 'Permission rejected.' : 'Permission granted.')
    await loadOpenCodePermissions()
    await loadOpenCodeMessages()
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Permission reply failed: ${error.message}`, true)
  } finally {
    openCodePermissionReplying = ''
    renderOpenCodePermissions()
  }
}

/** @param {string} id */
function selectOpenCodeSession(id) {
  if (id === openCodeSessionId) return
  debugLog('oc', `select ${shortId(id)}`)
  openCodeSessionId = id
  openCodeMessages = []
  openCodePermissions = []
  openCodeMessageSignature = OPENCODE_MESSAGE_STALE
  openCodeOlderCursor = ''
  openCodeLoadedOlder = false
  openCodeStick = true
  closeOpenCodeCommands()
  renderOpenCodeSessions()
  renderOpenCodeMessages()
  renderOpenCodePermissions()
  void loadOpenCodeMessages()
  void loadOpenCodePermissions()
  void ensureOpenCodeCommands()
}

async function sendOpenCodePrompt() {
  if (openCodeSending) return
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  const input = inputEl('oc-prompt')
  const text = input.value.trim()
  if (!text) return
  if (!openCodeSessionId) {
    toast('Select an OpenCode chat first.', true)
    return
  }
  closeOpenCodeCommands()
  openCodeSending = true
  buttonEl('btn-oc-send').disabled = true
  try {
    await ensureOpenCodeCommands()
    const command = parseOpenCodeCommand(text)
    if (command) {
      await api(`/api/v1/opencode/sessions/${encodeURIComponent(openCodeSessionId)}/command`, {
        method: 'POST',
        body: command
      })
      debugLog('oc', `command /${command.name}`)
    } else {
      await api(`/api/v1/opencode/sessions/${encodeURIComponent(openCodeSessionId)}/prompt`, {
        method: 'POST',
        body: { text }
      })
    }
    input.value = ''
    input.style.height = ''
    openCodeStick = true
    await loadOpenCodeMessages()
    await loadOpenCodeSessions()
    if (!isMobile()) input.focus()
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Send failed: ${error.message}`, true)
  } finally {
    openCodeSending = false
    buttonEl('btn-oc-send').disabled = false
  }
}

async function interruptOpenCode() {
  if (openCodeInterrupting || !openCodeSessionId) return
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  const ok = await confirmAction({
    title: 'Interrupt OpenCode?',
    text: 'Stop the current OpenCode turn. The conversation stays; only the running turn is interrupted.',
    ok: 'Interrupt'
  })
  if (!ok) return
  openCodeInterrupting = true
  buttonEl('btn-oc-interrupt').disabled = true
  try {
    await api(`/api/v1/opencode/sessions/${encodeURIComponent(openCodeSessionId)}/interrupt`, { method: 'POST' })
    toast('OpenCode turn interrupted.')
    await loadOpenCodeMessages()
    await loadOpenCodeSessions()
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Interrupt failed: ${error.message}`, true)
  } finally {
    openCodeInterrupting = false
    buttonEl('btn-oc-interrupt').disabled = false
  }
}

function fillOpenCodeWorktreeSelect(select, preferred) {
  select.replaceChildren()
  for (const wt of state.worktrees) {
    const option = el('option', '', worktreeLabel(wt))
    option.value = wt.id
    select.append(option)
  }
  if (preferred && state.worktrees.some((wt) => wt.id === preferred)) select.value = preferred
}

function openOpenCodeNewDialog() {
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  if (!state.opencode) {
    toast('OpenCode is not configured.', true)
    return
  }
  if (!state.worktrees.length) {
    toast('Add a repo and open a workspace first.', true)
    return
  }
  const form = formEl('form-oc-new')
  form.reset()
  const preferred = scopedWorktreeId() || state.worktrees[state.worktrees.length - 1].id
  fillOpenCodeWorktreeSelect(selectEl('oc-new-wt'), preferred)
  openDialog('dlg-oc-new')
  requestAnimationFrame(() => {
    debugOpenCodeBox('newstart', dialogEl('dlg-oc-new')?.querySelector('button[type="submit"]') || null)
  })
}

function renderSessionTabs() {
  const wrap = $('session-tabs')
  const scrollLeft = wrap.scrollLeft
  wrap.replaceChildren()
  if (!currentSession) {
    wrap.scrollLeft = scrollLeft
    return
  }

  const sessions = sessionsFor(currentSession.worktreeId)
  for (const sess of sessions) {
    const tab = el('div', 'session-tab')
    if (sess.id === currentSession.id) tab.classList.add('on')

    const main = el('button', 'session-tab-main')
    main.type = 'button'
    main.dataset.session = sess.id
    main.setAttribute('role', 'tab')
    main.setAttribute('aria-selected', String(sess.id === currentSession.id))
    main.title = `${sess.title || agentLabel(sess)} · ${sess.status}`
    main.append(sessionActivity(sess))
    main.append(el('span', 'session-tab-label', sess.title || agentLabel(sess)))
    tab.append(main)

    const close = el('button', 'session-tab-close', '×')
    close.type = 'button'
    close.dataset.sessionClose = sess.id
    close.title = sess.live ? 'Stop session' : 'Remove session'
    close.setAttribute('aria-label', `${sess.live ? 'Stop' : 'Remove'} ${sess.title || agentLabel(sess)}`)
    tab.append(close)
    wrap.append(tab)
  }

  const add = el('button', 'session-tab-add', '+')
  add.type = 'button'
  add.dataset.sessionAdd = '1'
  add.hidden = !hasTerminal()
  add.title = 'New session in this worktree or workspace'
  add.setAttribute('aria-label', 'New session in this worktree or workspace')
  wrap.append(add)
  wrap.scrollLeft = scrollLeft
  // A newly created session is appended at the end of a scrolled strip; a
  // phone cannot see the tab it just opened. Keep the active tab visible while
  // leaving the pinned add control's width alone.
  const active = /** @type {HTMLElement|null} */ (wrap.querySelector('.session-tab.on'))
  if (active) {
    const left = active.offsetLeft
    const right = left + active.offsetWidth
    const pinned = hasTerminal() ? 44 : 0
    if (left < wrap.scrollLeft) wrap.scrollLeft = left
    else if (right > wrap.scrollLeft + wrap.clientWidth - pinned) {
      wrap.scrollLeft = right - wrap.clientWidth + pinned
    }
  }
}

function renderSession() {
  const sess = currentSession
  if (!sess) return
  const wt = worktreeFor(sess.worktreeId)
  const repo = wt ? repoFor(wt.repoId) : null

  renderSessionTabs()

  const blocked = !hasTerminal()
  $('terminal-pane').hidden = blocked
  $('terminal-blocked').hidden = !blocked
  // A phone hides the session head on a live session. Without this action the
  // read-only panel would be a dead end: Chat lives in the hidden terminal
  // head, and the session's worktree is the only scope the panel needs.
  const blockedChat = buttonEl('btn-blocked-chat')
  blockedChat.hidden = !blocked || !state.opencode || !sess.worktreeId
  blockedChat.disabled = connState !== 'connected'
  buttonEl('btn-new-session').hidden = blocked
  const nativeButton = buttonEl('btn-term-native')
  const host = window.location.hostname
  // The Ghostty window is host-local and version-gated. Hide the action unless
  // this daemon reports a usable 1.3.1 on a graphical Linux session; the
  // browser terminal and the desktop VTE pane do not need it.
  const nativeAvailable = state.nativeTerminal?.available === true
  nativeButton.hidden = blocked || isMobile() || nativePaneActive || !nativeAvailable || !(host === 'localhost' || host === '[::1]' || host.startsWith('127.'))
  nativeButton.disabled = connState !== 'connected' || !sess.live || readOnly()

  $('session-crumb').textContent = `${repo ? repo.name : 'repo'} · ${wt ? wtName(wt) : 'worktree'}`
  $('session-title').textContent = sess.title || agentLabel(sess)

  const chips = $('session-chips')
  chips.replaceChildren()
  chips.append(statusPill(sess))
  chips.append(pill(agentLabel(sess), 'info'))
  chips.append(pill(`id ${shortId(sess.id)}`))
  if (sess.pid) chips.append(pill(`pid ${sess.pid}`))
  if (sess.containment) chips.append(pill(`containment ${sess.containment}`, 'warn'))
  if (sess.cols && sess.rows) chips.append(pill(`${sess.cols}×${sess.rows}`))
  if (readOnly()) chips.append(pill('read-only', 'warn'))

  const restart = buttonEl('btn-restart-session')
  restart.hidden = sess.live || !hasTerminal()
  restart.disabled = connState !== 'connected' || readOnly()
  restart.title = readOnly()
    ? 'This device is read-only'
    : connState !== 'connected'
      ? 'Reconnect to the daemon first'
      : 'Start a new PTY in this workspace; the old output is not persisted'

  const stop = buttonEl('btn-stop-session')
  stop.disabled = connState !== 'connected' || readOnly()
  stop.textContent = sess.live ? 'Stop' : 'Remove'
  stop.title = readOnly()
    ? 'This device is read-only'
    : connState !== 'connected'
      ? 'Reconnect to the daemon first'
      : sess.live ? 'Stop this session and verify the process group is gone' : 'Remove this saved session row'
  $('term-dot').replaceChildren(sessionActivity(sess))
  $('term-status').textContent = sess.live
    ? (streaming ? 'streaming' : detached ? 'detached' : 'attached')
    : (sess.status || 'exited')
  const ended = sess.endedAt ? ` · ended ${timeShort(sess.endedAt)}${sess.reason ? ` (${sess.reason})` : ''}` : ''
  $('term-meta').textContent = `${agentLabel(sess)}${sess.pid ? ` · pid ${sess.pid}` : ''} · ${timeShort(sess.createdAt)}${ended}`
  if (terminal) terminal.options.disableStdin = readOnly() || !sess.live || detached
  // A live session needs the tabs and the terminal, not a title row and three
  // buttons it cannot use; the phone reclaims that height. An exited session
  // keeps the row so Restart stays reachable.
  $('app').dataset.live = sess.live ? '1' : '0'
  const input = inputEl('term-input')
  input.disabled = readOnly() || !sess.live || detached
  input.placeholder = readOnly()
    ? 'This device is read-only'
    : !sess.live
      ? 'No live PTY; tap Restart to start one'
      : detached
        ? 'Detached; tap the connection pill to take over'
        : 'Type a line for the PTY'
  buttonEl('btn-term-send').disabled = input.disabled
  for (const key of document.querySelectorAll('[data-term-key]')) {
    /** @type {HTMLButtonElement} */ (key).disabled = input.disabled
  }
}

/* ---------- terminal ---------- */

function setTuiSession(value) {
  tuiSession = value
  const app = $('app')
  if (value) app.dataset.term = 'tui'
  else delete app.dataset.term
  // A TUI paints its own cursor and owns its history. xterm's blinking bar
  // would only add a full-row repaint every half second on the iPad, and a
  // large xterm scrollback is wasted DOM work when scrolling is PageUp/PageDown.
  if (terminal) {
    terminal.options.cursorBlink = !value
    terminal.options.scrollback = value ? 0 : 5000
  }
}

function tuiScrollMode() {
  // The server knows whether the attach looked like a full-screen TUI. Do not
  // rely on xterm's alternate-screen bit: OpenCode paints on the normal buffer
  // and still has no usable scrollback.
  return tuiSession || terminal?.buffer.active.type === 'alternate'
}

function sendTermScroll(sequence) {
  if (readOnly() || !streaming || detached || !currentSession?.live || terminalSessionId !== currentSession.id) return
  queueInput(sequence)
}

function terminalDetachedToast() {
  if (!detached) return false
  toast('This tab is detached. Tap the connection pill to take over.', true)
  return true
}

function scrollTerminal(direction) {
  if (!terminal) return
  if (terminalDetachedToast()) return
  if (tuiScrollMode()) {
    sendTermScroll(direction < 0 ? '\x1b[5~' : '\x1b[6~')
    return
  }
  terminal.scrollPages(direction)
}

function scrollTerminalLatest() {
  if (!terminal) return
  if (terminalDetachedToast()) return
  if (tuiScrollMode()) {
    // OpenCode binds End to its "navigate to last message" action. This is
    // the keyboard equivalent of its on-screen "Jump to latest" control,
    // which an iPad cannot click because xterm does not synthesize a mouse.
    sendTermScroll('\x1b[F')
    return
  }
  terminal.scrollToBottom()
}

// The WebGL addon does not expose its context, so read the canvas it created.
// This is the only way to tell a real GPU from SwiftShader/llvmpipe in a
// WebKit or Chromium webview, where "the renderer is enabled" is not a fact.
function webglRendererName() {
  const canvases = terminal?.element?.querySelectorAll('canvas') || []
  for (const canvas of canvases) {
    try {
      const gl = canvas.getContext('webgl2') || canvas.getContext('webgl')
      if (!gl) continue
      const info = gl.getExtension('WEBGL_debug_renderer_info')
      const name = info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER)
      if (typeof name === 'string' && name) return name
    } catch (error) {
      return `unreadable: ${error.message}`
    }
  }
  return ''
}

// A software GL context is slower than the DOM renderer for the parse-heavy
// workload (the clientbench control is ~4x slower at parse). Do not keep it
// just because the context exists; WebKitGTK and headless Chromium can both
// hand out SwiftShader/llvmpipe.
function isSoftwareRenderer(name) {
  return /swiftshader|llvmpipe|softpipe|software|basic render/i.test(name)
}

// WebKitGTK (the Wails shell on Linux) reports a masked "Apple GPU" renderer
// and its WebGL path was not a win: on the 1 MiB clientbench burst the DOM
// final-render range was 3.6-8.6 s and WebGL 3.6-21.0 s, while parse was
// ~3.57 s for both. Keep the DOM there until a real workload proves otherwise.
// iOS/macOS WebKit has no "Linux" token, so it still gets WebGL.
function webglPlatformAllows() {
  const ua = navigator.userAgent
  return !(/AppleWebKit/.test(ua) && /Linux/.test(ua) && !/Chrome|Chromium|Edg\//.test(ua))
}

function attachWebglRenderer() {
  if (webglAttached || !terminal) return
  const webglGlobal = /** @type {any} */ (globalThis).WebglAddon
  const WebglAddonCtor = typeof webglGlobal === 'function' ? webglGlobal : webglGlobal?.WebglAddon
  if (typeof WebglAddonCtor !== 'function') {
    debugLog('term', 'webgl addon missing after load')
    return
  }
  try {
    const webgl = new WebglAddonCtor()
    webgl.onContextLoss(() => {
      debugLog('term', 'webgl context lost; falling back to DOM')
      webglAttached = false
      // A lost context leaves an empty canvas under the same grid size. Force
      // the next fit to run so it repaints the visible rows.
      lastFitKey = ''
      try {
        webgl.dispose()
      } catch (error) {
        debugLog('term', `webgl dispose: ${error.message}`)
      }
    })
    terminal.loadAddon(webgl)
    webglAttached = true
    refreshTerminal()
    window.setTimeout(() => {
      refreshTerminal()
      const dpr = window.devicePixelRatio || 1
      const name = webglRendererName()
      debugLog('term', `webgl renderer: ${name || 'unknown'} dpr=${dpr.toFixed(2)}`)
      if (!isSoftwareRenderer(name)) return
      debugLog('term', `webgl is software (${name}); falling back to DOM`)
      webglAttached = false
      lastFitKey = ''
      try {
        webgl.dispose()
      } catch (error) {
        debugLog('term', `webgl dispose: ${error.message}`)
      }
      refreshTerminal()
    }, 120)
  } catch (error) {
    debugLog('term', `webgl unavailable: ${error.message}`)
  }
}

function ensureWebglAddon() {
  if (webglLoadStarted || webglAttached) return
  if (!webglPlatformAllows()) {
    debugLog('term', 'webgl skipped: WebKitGTK uses the DOM renderer')
    return
  }
  // A fractional devicePixelRatio (the Pixel 5 AVD reports 2.7272) makes the
  // WebGL renderer round the cell box, and glyphs get clipped between rows --
  // visible as text with its top and bottom sliced off. The DOM renderer does
  // not do that cell math, so keep it. This applies to desktop too: a 125% or
  // 150% display is the same cell-rounding case, not a phone-only one.
  const dpr = window.devicePixelRatio || 1
  if (Math.abs(dpr - Math.round(dpr)) > 0.02) {
    debugLog('term', `webgl skipped: fractional dpr ${dpr.toFixed(3)}`)
    return
  }
  const webglGlobal = /** @type {any} */ (globalThis).WebglAddon
  if (webglGlobal) {
    attachWebglRenderer()
    return
  }
  // Same-origin and covered by default-src 'self'; no inline code is needed.
  // The DOM renderer stays active until this loads, and again after a loss.
  webglLoadStarted = true
  const script = document.createElement('script')
  script.src = `/vendor/addon-webgl.js?v=${UI_VERSION}`
  script.async = true
  script.onload = () => { attachWebglRenderer() }
  script.onerror = () => { debugLog('term', 'webgl addon failed to load') }
  document.head.appendChild(script)
}

function initTerminal() {
  if (terminal) return
  const fitGlobal = /** @type {any} */ (globalThis).FitAddon
  const FitAddonCtor = typeof fitGlobal === 'function' ? fitGlobal : fitGlobal?.FitAddon
  if (typeof Terminal !== 'function' || typeof FitAddonCtor !== 'function') {
    debugLog('error', 'xterm.js is not loaded')
    return
  }
  termFontSize = readTermFont()
  terminal = new Terminal({
    allowProposedApi: false,
    convertEol: false,
    cursorBlink: true,
    cursorStyle: 'bar',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: termFontSize,
    lineHeight: 1.2,
    scrollback: 5000,
    theme: {
      background: '#090c0f',
      foreground: '#c3cedb',
      cursor: '#7ee787',
      cursorAccent: '#090c0f',
      selectionBackground: '#264f78'
    },
    windowOptions: {},
    linkHandler: { activate() { /* a PTY must not open links */ } }
  })
  fitAddon = new FitAddonCtor()
  terminal.loadAddon(fitAddon)
  terminal.open($('terminal'))
  // The page is still hidden during boot, so terminal.open() may measure a
  // zero-size grid. A WebGL context created there can stay blank on Android
  // until a reload; attach the renderer after the first real fit instead.
  if ($('terminal').clientWidth > 0) ensureWebglAddon()
  terminal.onData((data) => queueInput(data))
  terminal.onResize(({ cols, rows }) => queueResize(cols, rows))
  terminal.onTitleChange(() => { /* a PTY title is never identity */ })
  // A full-screen TUI owns its scrollback, so xterm has nothing useful to
  // scroll. Capture the wheel before xterm and send PageUp/PageDown to the app.
  const termNode = $('terminal')
  termNode.addEventListener('wheel', (event) => {
    if (!tuiScrollMode()) return
    event.preventDefault()
    event.stopPropagation()
    sendTermScroll(event.deltaY < 0 ? '\x1b[5~' : '\x1b[6~')
    debugLog('term', event.deltaY < 0 ? 'tui wheel up' : 'tui wheel down')
  }, { passive: false, capture: true })
  // xterm.js 6.0.0 removed native touch scrolling (#5489). Translate a
  // vertical touch pan into a synthetic wheel event so it uses xterm's normal
  // pipeline: scrollback for a shell, SGR wheel reports for mouse-aware TUIs,
  // and arrow keys for an alt-screen pager without wheel support.
  let touchStartX = 0
  let touchStartY = 0
  let touchLastY = 0
  let touchAxis = ''
  let touchActive = false
  // A TUI has no scrollback; the wheel handler turns one event into one
  // PageUp/PageDown. Touchmove fires many times per swipe, so accumulate the
  // finger travel and page once per threshold instead of once per event.
  let touchPagePx = 0
  const TOUCH_TUI_PAGE_PX = 160
  const dispatchTerminalWheel = (touch, deltaY) => {
    if (!terminal || !deltaY) return
    // xterm 6 listens on the screen/rows layer, not the outer .xterm node.
    // Target it directly: elementFromPoint() mixes visual-viewport touch
    // coordinates with layout coordinates once the iPad keyboard shifts the
    // shell, which broke scrolling in landscape.
    const screen = terminal.element?.querySelector('.xterm-screen')
    const target = screen || terminal.element || termNode
    target.dispatchEvent(new WheelEvent('wheel', {
      deltaY,
      deltaMode: 0,
      clientX: touch.clientX,
      clientY: touch.clientY,
      bubbles: true,
      cancelable: true
    }))
  }
  termNode.addEventListener('touchstart', (event) => {
    if (event.touches.length !== 1) {
      touchActive = false
      return
    }
    const touch = event.touches[0]
    touchStartX = touch.clientX
    touchStartY = touch.clientY
    touchLastY = touch.clientY
    touchAxis = ''
    touchActive = true
    touchPagePx = 0
  }, { passive: true })
  termNode.addEventListener('touchmove', (event) => {
    if (!touchActive || event.touches.length !== 1) return
    const touch = event.touches[0]
    if (!touchAxis) {
      const dx = touch.clientX - touchStartX
      const dy = touch.clientY - touchStartY
      if (Math.abs(dx) < 8 && Math.abs(dy) < 8) return
      touchAxis = Math.abs(dy) >= Math.abs(dx) ? 'y' : 'x'
      touchLastY = touch.clientY
      if (touchAxis !== 'y') {
        touchActive = false
        return
      }
      return
    }
    if (touchAxis !== 'y') return
    const deltaY = touchLastY - touch.clientY
    touchLastY = touch.clientY
    if (deltaY) {
      if (tuiScrollMode()) {
        touchPagePx += deltaY
        if (Math.abs(touchPagePx) >= TOUCH_TUI_PAGE_PX) {
          dispatchTerminalWheel(touch, touchPagePx < 0 ? -1 : 1)
          touchPagePx = 0
        }
      } else {
        dispatchTerminalWheel(touch, deltaY)
      }
    }
    if (event.cancelable) event.preventDefault()
    event.stopPropagation()
  }, { passive: false, capture: true })
  termNode.addEventListener('touchend', () => {
    touchActive = false
    touchAxis = ''
  }, { passive: true })
  termNode.addEventListener('touchcancel', () => {
    touchActive = false
    touchAxis = ''
  }, { passive: true })
  if ('ResizeObserver' in window) {
    new ResizeObserver(() => {
      scheduleFit()
      scheduleNativeBounds()
    }).observe($('terminal'))
  }
  // Tapping the terminal has to raise the keyboard, but touching it to scroll
  // must not: on iPad a focus here opens the keyboard mid-gesture, the visual
  // viewport shrinks under the finger, and the pan turns into a jump. Defer
  // touch focus to a confirmed tap; a mouse or pen still focuses on press.
  const focusTerminal = () => {
    if (!terminal) return
    if (document.activeElement?.classList.contains('xterm-helper-textarea') === true) return
    terminal.focus()
  }
  let tapPointerId = -1
  let tapStartX = 0
  let tapStartY = 0
  let tapStartedAt = 0
  termNode.addEventListener('pointerdown', (event) => {
    if (event.pointerType !== 'touch') {
      focusTerminal()
      return
    }
    tapPointerId = event.pointerId
    tapStartX = event.clientX
    tapStartY = event.clientY
    tapStartedAt = performance.now()
  }, { passive: true })
  termNode.addEventListener('pointerup', (event) => {
    if (event.pointerType !== 'touch' || event.pointerId !== tapPointerId) return
    tapPointerId = -1
    const moved = Math.abs(event.clientX - tapStartX) > 10 || Math.abs(event.clientY - tapStartY) > 10
    if (moved || performance.now() - tapStartedAt > 800) return
    focusTerminal()
  }, { passive: true })
  termNode.addEventListener('pointercancel', () => { tapPointerId = -1 }, { passive: true })
  debugLog('term', `xterm ready cols=${terminal.cols} rows=${terminal.rows}`)
}

function adjustTermFont(delta) {
  if (!terminal) return
  const next = Math.min(TERM_FONT_MAX, Math.max(TERM_FONT_MIN, (terminal.options.fontSize || TERM_FONT_DEFAULT) + delta))
  if (next === terminal.options.fontSize) return
  terminal.options.fontSize = next
  termFontSize = next
  saveTermFont(next)
  scheduleFit()
}

function exitZen() {
  const app = $('app')
  if (app.dataset.zen !== '1') return
  delete app.dataset.zen
  const button = $('btn-term-focus')
  button.textContent = 'Focus'
  button.title = 'Focus mode: hide the fleet chrome'
  button.setAttribute('aria-label', 'Enter terminal focus mode')
  scheduleFit()
}

function toggleZen() {
  if (!currentSession) return
  const app = $('app')
  if (app.dataset.zen === '1') {
    exitZen()
    return
  }
  app.dataset.zen = '1'
  const button = $('btn-term-focus')
  button.textContent = 'Exit'
  button.title = 'Exit focus mode'
  button.setAttribute('aria-label', 'Exit terminal focus mode')
  scheduleFit()
  if (terminal && !isMobile()) terminal.focus()
}

function resetTerminal() {
  terminalGeneration++
  terminalInputError = false
  clearPendingInput()
  if (termWriteFrame) {
    window.cancelAnimationFrame(termWriteFrame)
    termWriteFrame = 0
  }
  termWriteQueue = ''
  oscCarry = ''
  releaseTermDrain()
  if (!terminal) return
  lastFitKey = ''
  terminal.reset()
  terminal.clear()
}

function releaseTermDrain() {
  if (termWriteQueue.length > TERM_QUEUE_HIGH) return
  const waiters = termDrainWaiters
  termDrainWaiters = []
  for (const resolve of waiters) resolve()
}

function flushTermWrites() {
  if (termWriteFrame) {
    window.cancelAnimationFrame(termWriteFrame)
    termWriteFrame = 0
  }
  const data = termWriteQueue
  termWriteQueue = ''
  if (data && terminal) terminal.write(data)
  releaseTermDrain()
}

function waitForTermQueue() {
  if (termWriteQueue.length <= TERM_QUEUE_HIGH) return Promise.resolve()
  return /** @type {Promise<void>} */ (new Promise((resolve) => { termDrainWaiters.push(() => resolve()) }))
}

function writeTerm(text) {
  if (!terminal || !text) return
  termWriteQueue += text
  // Interactive echo is a small chunk: writing it now instead of on the next
  // frame removes a vsync wait from every keystroke. A burst still coalesces
  // into one write per frame, so the parser is not asked to handle one network
  // chunk at a time.
  if (document.hidden || termWriteQueue.length <= TERM_WRITE_NOW) {
    flushTermWrites()
    return
  }
  if (!termWriteFrame) termWriteFrame = window.requestAnimationFrame(flushTermWrites)
}

// Output must not be able to repaint the terminal theme. OSC 4/10/11/12 set the
// palette and the default foreground, background and cursor, which is enough to
// hide text (foreground equal to background) or to impersonate another agent's
// prompt colours. Queries still pass so a TUI can detect the palette; sets and
// the reset forms (104/110/111/112) are dropped. Everything else passes through
// byte for byte, and a chunk with no OSC introducer takes one indexOf.
const PALETTE_OSC = new Set(['4', '10', '11', '12', '104', '110', '111', '112'])
const PALETTE_OSC_RESET = new Set(['104', '110', '111', '112'])
// A sequence split across stream chunks is held until its terminator arrives.
// Past this size the partial sequence is dropped rather than buffered.
const OSC_CARRY_MAX = 4096
let oscCarry = ''

/** @param {string} source @param {number} from */
function oscEnd(source, from) {
  for (let i = from; i < source.length; i++) {
    if (source[i] === '\x07') return { index: i, next: i + 1 }
    if (source[i] === '\x1b' && source[i + 1] === '\\') return { index: i, next: i + 2 }
  }
  return null
}

// OSC 4 is "4;index;spec;index;spec"; 10/11/12 carry a single spec. A query is
// the '?' spec. A mixed sequence that sets anything is dropped as a whole.
function paletteOscIsQuery(payload) {
  const parts = payload.split(';')
  if (parts[0] === '4') {
    const specs = parts.slice(2).filter((_, index) => index % 2 === 0)
    return specs.length > 0 && specs.every((spec) => spec === '?')
  }
  if (PALETTE_OSC_RESET.has(parts[0])) return false
  return parts.length > 1 && parts.slice(1).every((spec) => spec === '?')
}

function filterPaletteOsc(text) {
  if (!oscCarry && !text.includes('\x1b]')) return text
  let source = oscCarry + text
  oscCarry = ''
  let out = ''
  let at = 0
  while (at < source.length) {
    const start = source.indexOf('\x1b]', at)
    if (start === -1) {
      let tail = source.slice(at)
      // A chunk can end between ESC and ']'; hold the ESC so the split
      // introducer is still recognised on the next chunk.
      if (tail.endsWith('\x1b')) {
        oscCarry = '\x1b'
        tail = tail.slice(0, -1)
      }
      out += tail
      break
    }
    out += source.slice(at, start)
    const end = oscEnd(source, start + 2)
    if (!end) {
      const rest = source.slice(start)
      if (rest.length <= OSC_CARRY_MAX) oscCarry = rest
      return out
    }
    const payload = source.slice(start + 2, end.index)
    if (!PALETTE_OSC.has(payload.split(';', 1)[0]) || paletteOscIsQuery(payload)) {
      out += source.slice(start, end.next)
    }
    at = end.next
  }
  return out
}

function writeSession(sessionId, text) {
  if (currentSession?.id !== sessionId) return
  writeTerm(filterPaletteOsc(text))
}

function clearPendingInput() {
  inputFlushScheduled = false
  inputRetryCount = 0
  if (inputRetryTimer) {
    window.clearTimeout(inputRetryTimer)
    inputRetryTimer = 0
  }
  pendingInput = ''
}

// A keystroke must not wait for a timer. The request leaves in the microtask
// checkpoint after this task: a paste or a burst still coalesces into one
// request, but the 4-8 ms a setTimeout(0) costs before the next frame is gone.
function scheduleInputFlush() {
  if (inputInFlight || inputFlushScheduled) return
  inputFlushScheduled = true
  queueMicrotask(() => {
    inputFlushScheduled = false
    void flushInput()
  })
}

async function flushInput() {
  if (inputInFlight || inputRetryTimer || !pendingInput) return
  const sessionId = currentSession?.id
  if (!sessionId || !currentSession.live || terminalSessionId !== sessionId) {
    pendingInput = ''
    return
  }
  const data = pendingInput
  pendingInput = ''
  inputInFlight = true
  let retryDelay = 0
  try {
    await api(`/api/v1/sessions/${encodeURIComponent(sessionId)}/input`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: data
    })
    inputRetryCount = 0
  } catch (error) {
    if (error.message === 'unauthorized') return
    // 429 is the daemon's envelope budget, 502 is the Android proxy when the
    // host did not answer: both are transient and the bytes are still valid.
    const retryable = error.status === 429 || error.status === 502 ||
      error.message === 'daemon unreachable'
    if (retryable && inputRetryCount < 4 && terminalSessionId === sessionId) {
      inputRetryCount++
      retryDelay = 120 * inputRetryCount
      // Keep the user's order: the failed batch goes before newer keystrokes.
      pendingInput = data + pendingInput
      debugLog('term', `input retry ${inputRetryCount} in ${retryDelay}ms: ${error.message}`)
    } else {
      inputRetryCount = 0
      if (!terminalInputError) {
        terminalInputError = true
        toast(`input: ${error.message}`, true)
      }
    }
  } finally {
    inputInFlight = false
    if (retryDelay) {
      inputRetryTimer = window.setTimeout(() => {
        inputRetryTimer = 0
        scheduleInputFlush()
      }, retryDelay)
    } else if (pendingInput) {
      scheduleInputFlush()
    }
  }
}

function queueInput(data) {
  if (!data || detached || !currentSession?.live || terminalSessionId !== currentSession.id) return
  pendingInput += data
  scheduleInputFlush()
}

function queueResize(cols, rows) {
  if (!resizeReady || !currentSession?.live) return
  const sessionId = currentSession.id
  const generation = terminalGeneration
  pendingResize = { cols, rows }
  if (resizeSendTimer) return
  resizeSendTimer = window.setTimeout(() => {
    resizeSendTimer = 0
    const size = pendingResize
    pendingResize = null
    if (!size || !resizeReady || generation !== terminalGeneration || !currentSession?.live || currentSession.id !== sessionId) return
    api(`/api/v1/sessions/${encodeURIComponent(sessionId)}/resize`, {
      method: 'POST',
      body: size
    }).catch((error) => {
      if (error.message !== 'unauthorized') debugLog('term', `resize: ${error.message}`)
    })
  }, 80)
}

async function requestRedraw(sessionId) {
  try {
    await api(`/api/v1/sessions/${encodeURIComponent(sessionId)}/redraw`, { method: 'POST' })
  } catch (error) {
    if (error.message !== 'unauthorized') debugLog('term', `redraw: ${error.message}`)
  }
}

function refreshTerminal() {
  if (!terminal) return
  window.requestAnimationFrame(() => {
    if (terminal) terminal.refresh(0, terminal.rows - 1)
  })
}

// A view that was display:none while the terminal initialized can come back
// with a stale grid and an unpainted canvas: the phone shows a faint layout
// until a manual view switch. Two frames guarantee the browser has laid the
// terminal out before the fit and repaint run.
function refreshTerminalSoon() {
  if (!terminal) return
  window.requestAnimationFrame(() => {
    window.requestAnimationFrame(() => {
      if (!terminal || selection?.kind !== 'session') return
      lastFitKey = ''
      fitTerminal()
      refreshTerminal()
      scheduleNativeBounds()
    })
  })
}

// The explicit control behind the "sometimes needs a refresh" report: refit,
// repaint, and ask the PTY for one SIGWINCH so a full-screen TUI redraws even
// when the canvas kept a stale frame. Never resizes the PTY.
function refreshTerminalNow() {
  if (!terminal) return
  lastFitKey = ''
  fitTerminal()
  refreshTerminal()
  scheduleNativeBounds()
  if (currentSession?.id && terminalSessionId === currentSession.id) {
    void requestRedraw(currentSession.id)
  }
}

// A blank canvas is what the user sees as "black until I type". A TUI attach
// replays nothing and relies on a SIGWINCH repaint, which some apps deliver
// only on their next event, so check the buffer instead of counting bytes: a
// single cursor update still leaves an empty screen.
function terminalLooksBlank() {
  if (!terminal) return true
  const buffer = terminal.buffer.active
  const end = Math.min(buffer.length, buffer.viewportY + terminal.rows)
  for (let y = buffer.viewportY; y < end; y++) {
    const line = buffer.getLine(y)
    if (line && line.translateToString(true)) return false
  }
  return true
}

function fitTerminal() {
  if (!terminal || !fitAddon) return false
  const node = $('terminal')
  if (node.clientWidth < 40 || node.clientHeight < 40) return false
  try {
    const proposed = fitAddon.proposeDimensions()
    const key = proposed ? `${proposed.cols}x${proposed.rows}` : ''
    if (key && key === lastFitKey) return false
    fitAddon.fit()
    lastFitKey = `${terminal.cols}x${terminal.rows}`
    // The first real fit is also the first moment a WebGL context can be sized
    // correctly; attaching it while the app was hidden left some Android
    // WebViews with a blank canvas until a manual refresh.
    ensureWebglAddon()
    // iOS can leave the canvas blank after the keyboard changes the row count
    // until the next write. Repaint only after a real grid change.
    refreshTerminal()
    return true
  } catch (error) {
    debugLog('term', `fit: ${error.message}`)
    return false
  }
}

function scheduleFit() {
  // Never fit from a timer: the keyboard animation reports its final size late.
  // The settle poll fits once the reported size holds still for two frames.
  settleTerminalViewport()
}

// The desktop shell can put a native VTE terminal over the web terminal. It
// marks the page after the window exists; a browser never has this flag, so
// the fallback stays the xterm plus the separate Ghostty window.
const nativeWindow = /** @type {any} */ (window)

function nativeShell() {
  if (nativeDisabled) return false
  return new URLSearchParams(location.search).get('native') === '1'
}

// Wails injects its JS runtime into the page; it can be missing for the first
// moments after a load. Wait for it before promising the shell a pane.
async function nativeRuntimeReady(timeout = 1500) {
  const start = performance.now()
  while (performance.now() - start < timeout) {
    if (nativeWindow.__dfNativeSend) return true
    await new Promise((resolve) => setTimeout(resolve, 50))
  }
  return false
}

function emitNative(type, data) {
  try {
    nativeWindow.__dfNativeSend?.({ type, ...data })
  } catch (error) {
    debugLog('term', `native ${type}: ${error.message}`)
  }
}

function nativeBounds() {
  const node = $('terminal')
  if (!node) return null
  const rect = node.getBoundingClientRect()
  if (rect.width < 40 || rect.height < 40) return null
  return {
    x: Math.round(rect.left),
    y: Math.round(rect.top),
    w: Math.round(rect.width),
    h: Math.round(rect.height)
  }
}

function scheduleNativeBounds() {
  if (!nativePaneActive || nativeBoundsTimer) return
  nativeBoundsTimer = window.requestAnimationFrame(() => {
    nativeBoundsTimer = 0
    if (!nativePaneActive) return
    const bounds = nativeBounds()
    if (bounds) emitNative('bounds', bounds)
    // The same frame decides visibility: a layout change that hid the terminal
    // must hide the widget, and one that brought it back must show it again.
    syncNativePaneVisibility()
  })
}

function closeNativePane() {
  if (!nativePaneActive && !nativePaneSession) return
  nativePaneActive = false
  nativePaneSession = ''
  nativePaneHidden = false
  emitNative('close', {})
}

// The pane is a GTK overlay child, so it always paints above the WebView: an
// HTML modal dialog under it is invisible and unclickable while a terminal
// session is open, which reads as a hang. Hide the widget, do not detach it —
// the PTY stays attached and VTE keeps its screen, so showing it again costs
// nothing. VTE also sheds keyboard focus while hidden, so the dialog's fields
// take input.
function syncNativePaneVisibility() {
  if (!nativePaneSession) return
  // The activity list is an overlay too: it hangs under the bell and its lower
  // half lands inside the terminal rectangle, where the GTK pane paints above
  // the WebView. Hiding the pane while it is open keeps the whole list usable.
  // A view that is not the terminal collapses the element to nothing; an
  // overlay child left at the old rectangle paints a dead dark block over the
  // chat or fleet that replaced it.
  const hidden = Boolean(document.querySelector('dialog[open]')) || attentionOpen || nativeBounds() === null
  if (hidden === nativePaneHidden) return
  nativePaneHidden = hidden
  debugLog('term', `native pane ${hidden ? 'hidden' : 'shown'}`)
  emitNative(hidden ? 'hide' : 'show', {})
}

// The shell calls these from ExecJS after the VTE overlay is up (or has
// failed). A failed pane falls back to the browser stream for this session.
nativeWindow.__dfTerminalReady = () => {
  if (!nativePaneSession) return
  nativePaneActive = true
  renderSession()
  scheduleNativeBounds()
}

nativeWindow.__dfTerminalFailed = () => {
  if (!nativePaneSession) return
  const session = currentSession
  nativePaneSession = ''
  nativePaneActive = false
  emitNative('close', {})
  nativeDisabled = true
  if (session) openSession(session)
}

async function openNativePane(session) {
  const bounds = nativeBounds()
  if (!bounds) return false
  if (!(await nativeRuntimeReady())) {
    debugLog('term', 'native shell runtime never loaded')
    return false
  }
  try {
    /** @type {Record<string, string>} */
    const headers = { 'X-DF-Request': '1' }
    if (token) headers.Authorization = `Bearer ${token}`
    const response = await fetch(`/api/v1/sessions/${encodeURIComponent(session.id)}/native-pane`, {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      cache: 'no-store'
    })
    if (!response.ok) {
      debugLog('term', `native pane refused ${response.status}`)
      return false
    }
    const body = await response.json()
    if (!body || typeof body.pane !== 'string' || !body.pane) return false
    nativePaneSession = session.id
    emitNative('open', { sessionId: session.id, pane: body.pane, ...bounds })
    // A dialog may already be up (a session started from one); the pane must
    // not cover it. Messages arrive in order, so the shell applies this after
    // it has created the widget.
    nativePaneHidden = false
    syncNativePaneVisibility()
    return true
  } catch (error) {
    debugLog('term', `native pane: ${error.message}`)
    return false
  }
}

function stopStream() {
  if (nativePaneActive || nativePaneSession) closeNativePane()
  if (streamAbort) streamAbort.abort()
  streamAbort = null
  streaming = false
  resizeReady = false
  detached = false
  clearPendingInput()
  setTuiSession(false)
}

async function openSession(session) {
  if (!session) return
  if (!hasTerminal()) {
    stopStream()
    currentSession = sessionFor(session.id) || session
    selection = { kind: 'session', id: currentSession.id }
    expandedWorktrees.add(currentSession.worktreeId)
    if (isMobile()) setMobileView('session')
    renderTree()
    renderView()
    return
  }
  initTerminal()
  stopStream()
  const known = sessionFor(session.id)
  currentSession = known || session
  selection = { kind: 'session', id: currentSession.id }
  expandedWorktrees.add(currentSession.worktreeId)
  if (isMobile()) setMobileView('session')

  terminalSessionId = currentSession.id
  resetTerminal()
  if (terminal) terminal.options.disableStdin = readOnly() || !currentSession.live
  renderTree()
  renderView()
  scheduleFit()
  refreshTerminal()
  if (terminal && !isMobile()) terminal.focus()

  if (!currentSession.live) {
    writeTerm(`(session ${shortId(currentSession.id)} is ${currentSession.status}; the daemon has no live PTY for it)\r\n`)
    writeTerm('Restart starts a new PTY in this workspace; previous output is not persisted.\r\n')
    return
  }

  if (nativeShell() && !readOnly()) {
    const opened = await openNativePane(currentSession)
    if (opened) {
      // The native attach client owns the stream; do not race it with a
      // second browser viewer.
      streaming = false
      detached = false
      renderSession()
      scheduleNativeBounds()
      return
    }
  }

  const sessionId = currentSession.id
  const controller = new AbortController()
  streamAbort = controller
  streaming = true
  renderSession()

  try {
    /** @type {Record<string, string>} */
    const streamHeaders = {}
    if (token) streamHeaders.Authorization = `Bearer ${token}`
    const requestedAt = performance.now()
    debugLog('term', `stream open ${shortId(sessionId)}`)
    const response = await fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}/stream`, {
      headers: streamHeaders,
      credentials: 'same-origin',
      signal: controller.signal,
      cache: 'no-store'
    })
    debugLog('term', `stream headers ${response.status} after ${Math.round(performance.now() - requestedAt)}ms`)
    if (response.status === 401) {
      lostAuth()
      return
    }
    if (!response.ok || !response.body) {
      const refusal = await refusalFrom(response)
      throw new Error(refusalMessage(refusal, response.statusText))
    }
    // The stream attach has already detached any older browser. Only now may
    // this client resize the PTY: fitting before the attach would resize the
    // old client's screen and make its TUI look corrupted for a moment.
    setTuiSession(response.headers.get('X-DF-Redraw') === '1')
    debugLog('term', `attach ${shortId(sessionId)} redraw=${tuiSession ? 1 : 0}`)
    resizeReady = true
    fitTerminal()
    // Paint even when the fit was a no-op: a fresh canvas has nothing on it
    // until something refreshes it, and a TUI attach sends no replay.
    refreshTerminal()
    if (terminal) queueResize(terminal.cols, terminal.rows)
    // A full-screen TUI gets no ring replay; ask the PTY for a SIGWINCH
    // repaint so the fresh xterm is painted from the top instead of showing a
    // partial frame that starts inside an escape sequence.
    if (tuiSession) {
      void requestRedraw(sessionId)
      // The nudge is a SIGWINCH: an app that ignores it, or a nudge that lands
      // before the size is applied, leaves a black canvas until the first
      // keystroke. Check the screen itself, twice, and repaint.
      for (const delay of [700, 1600]) {
        window.setTimeout(() => {
          if (currentSession?.id !== sessionId || !terminal) return
          if (!tuiSession || !terminalLooksBlank()) return
          debugLog('term', `blank screen after attach; re-nudging redraw (${delay}ms)`)
          void requestRedraw(sessionId)
          refreshTerminal()
        }, delay)
      }
    }

    // A black pane has two very different causes: the bytes never reached
    // xterm's buffer, or they did and the renderer never painted them. One line
    // carrying both numbers tells them apart without guesswork.
    if (DEBUG) window.setTimeout(() => {
      if (currentSession?.id !== sessionId || !terminal) return
      const buffer = terminal.buffer.active
      let rows = 0
      const end = Math.min(buffer.length, buffer.viewportY + terminal.rows)
      for (let y = buffer.viewportY; y < end; y++) {
        const line = buffer.getLine(y)
        if (line && line.translateToString(true)) rows++
      }
      const rect = terminal.element?.getBoundingClientRect()
      const box = rect ? `${Math.round(rect.width)}x${Math.round(rect.height)}` : 'none'
      const modes = terminal.modes
      debugLog('term', `screen ${terminal.cols}x${terminal.rows} type=${buffer.type} len=${buffer.length} vp=${buffer.viewportY}/${buffer.baseY} rows=${rows} box=${box} mouse=${modes.mouseTrackingMode} cursor=${modes.applicationCursorKeysMode ? 'app' : 'normal'} paste=${modes.bracketedPasteMode ? 'on' : 'off'}`)
    }, 1200)

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let firstChunk = true
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      const text = decoder.decode(value, { stream: true })
      if (text) {
        if (firstChunk) {
          debugLog('term', `first chunk ${text.length} bytes`)
          firstChunk = false
        }
        writeSession(sessionId, text)
        // If the TUI is producing faster than the iPad can paint, stop
        // reading until the write queue drains. TCP backpressure then reaches
        // the PTY instead of turning into minutes of hidden lag.
        await waitForTermQueue()
      }
    }
    const tail = decoder.decode()
    if (tail) writeSession(sessionId, tail)
    writeSession(sessionId, '\r\n[stream closed]\r\n')
  } catch (error) {
    if (error.name !== 'AbortError') writeSession(sessionId, `\r\n[stream: ${error.message}]\r\n`)
  } finally {
    if (streamAbort === controller) {
      streamAbort = null
      streaming = false
      resizeReady = false
      setTuiSession(false)
      if (currentSession?.id === sessionId) {
        detached = true
        renderSession()
        toast('Terminal stream closed. Reconnect to attach.', true)
      }
    }
    void refresh()
  }
}

/* ---------- selection ---------- */

function setMobileView(name) {
  $('app').dataset.mobile = name
  const buttons = /** @type {NodeListOf<HTMLElement>} */ (document.querySelectorAll('.nav-btn'))
  buttons.forEach((button) => {
    button.classList.toggle('on', button.dataset.view === name)
  })
  // The session view owns a canvas that was hidden while Fleet (or another
  // view) was up. Re-fit and repaint after layout instead of waiting for the
  // next PTY byte, which is why a manual session switch used to be the only
  // way to clear a faint phone screen.
  if (name === 'session') refreshTerminalSoon()
}

/** @param {string} id @param {boolean} [keepChat] stay in the chat, re-scoped */
function selectWorktree(id, keepChat = true) {
  const wt = worktreeFor(id)
  if (!wt) return
  if (keepChat && selection?.kind === 'opencode') {
    // The chat belongs to the worktree, so switching worktree switches the
    // conversation instead of leaving the panel pointing at the old one.
    stopStream()
    currentSession = null
    terminalSessionId = ''
    resetTerminal()
    if (terminal) terminal.options.disableStdin = true
    scopeOpenCode(wt)
    selection = { kind: 'opencode', id: wt.id }
    if (isMobile()) setMobileView('opencode')
    renderTree()
    renderView()
    startOpenCodePolling()
    return
  }
  stopStream()
  currentSession = null
  terminalSessionId = ''
  resetTerminal()
  if (terminal) terminal.options.disableStdin = true
  selection = { kind: 'worktree', id }
  if (isMobile()) setMobileView('session')
  renderTree()
  renderView()
}

function showRuns() {
  stopStream()
  if (terminal) terminal.options.disableStdin = true
  selection = { kind: 'runs' }
  if (isMobile()) setMobileView('runs')
  renderTree()
  renderView()
}

// Which worktree a chat action refers to. A chat is always about one directory:
// the selected session's worktree, the selected worktree, or the worktree the
// panel is already scoped to. Fleet has no worktree, so it has no chat action.
function scopedWorktreeId() {
  if (selection?.kind === 'opencode') return openCodeScopeWorktreeId || selection.id || ''
  if (selection?.kind === 'session') return currentSession?.worktreeId || ''
  if (selection?.kind === 'worktree') return selection.id || ''
  return ''
}

// Starting a chat in a different worktree discards the previous worktree's
// selection: keeping a chat id from another directory would show messages that
// do not belong to the scope in the header.
function scopeOpenCode(wt) {
  openCodeScopeWorktreeId = wt.id
  openCodeScopeDirectory = wt.path || ''
  openCodeSessionId = ''
  openCodeMessages = []
  openCodePermissions = []
  openCodeMessageSignature = ''
  openCodeOlderCursor = ''
  openCodeLoadedOlder = false
  openCodeStick = true
  openCodeListSignature = ''
  openCodeSearch = ''
  inputEl('oc-search').value = ''
  closeOpenCodeCommands()
}

/** @param {string} [worktreeId] @returns {boolean} true when the chat is open */
function showOpenCode(worktreeId) {
  if (!state.opencode) {
    toast('OpenCode is not configured.', true)
    return false
  }
  const wt = worktreeFor(worktreeId || scopedWorktreeId())
  if (!wt) {
    toast('Open a worktree first; a chat belongs to one worktree.', true)
    return false
  }
  stopStream()
  if (terminal) terminal.options.disableStdin = true
  const scoped = selection?.kind === 'opencode' && openCodeScopeWorktreeId === wt.id
  if (!scoped || openCodeScopeDirectory !== (wt.path || '')) scopeOpenCode(wt)
  selection = { kind: 'opencode', id: wt.id }
  if (isMobile()) setMobileView('opencode')
  renderTree()
  renderView()
  startOpenCodePolling()
  return true
}

function showFleet() {
  stopStream()
  if (terminal) terminal.options.disableStdin = true
  selection = null
  if (isMobile()) setMobileView('fleet')
  renderTree()
  renderView()
}

function showSession() {
  if (currentSession) {
    if (!streaming) {
      void openSession(currentSession)
    } else {
      selection = { kind: 'session', id: currentSession.id }
      if (isMobile()) setMobileView('session')
      renderTree()
      renderView()
      scheduleFit()
      refreshTerminal()
    }
    return
  }
  if (selection?.kind === 'worktree') {
    if (isMobile()) setMobileView('session')
    return
  }
  if (state.worktrees.length) {
    selectWorktree(state.worktrees[state.worktrees.length - 1].id, false)
    return
  }
  if (isMobile()) setMobileView('session')
  renderView()
  toast('Add a repo and open a worktree or workspace first.', true)
}

/* ---------- chat mode ---------- */

// A layout mode over the existing OpenCode panel, not a second chat client.
// The terminal keeps its own Focus mode (data-zen); this one only ever applies
// to view-opencode, and only while the daemon reports OpenCode as configured.
/** @param {boolean} on */
function setChatLayout(on) {
  if (chatLayout === on && ($('app').dataset.chat === '1') === on) return
  chatLayout = on
  const app = $('app')
  if (on) app.dataset.chat = '1'
  else delete app.dataset.chat
  const button = $('btn-oc-mode')
  button.textContent = on ? 'Exit' : 'Chat'
  button.title = on ? 'Exit chat mode' : 'Chat mode: give the chat the whole screen'
  button.setAttribute('aria-label', on ? 'Exit chat mode' : 'Enter chat mode')
  button.setAttribute('aria-pressed', on ? 'true' : 'false')
  requestAnimationFrame(reportOpenCodeBoxes)
  // The state poll re-renders every few seconds; only a real transition is
  // worth a log line, or the client log fills with the same sentence.
  debugLog('oc', `chat mode ${on ? 'on' : 'off'}`)
}

/** @param {boolean} on @param {boolean} [persist] */
function applyChatMode(on, persist = true) {
  const enabled = Boolean(on && state.opencode)
  setChatLayout(enabled)
  if (persist) {
    chatMode = enabled
    saveChatMode(enabled)
  }
}

// Leaving the view clears the layout without forgetting the preference, so a
// comparison tab returns to the chat it was set up for. The button reflects the
// layout, not the preference; the preference re-applies on the next entry.
function exitChatMode() {
  if ($('app').dataset.chat !== '1') return
  setChatLayout(false)
}

function toggleChatMode() {
  if (!state.opencode) {
    toast('OpenCode is not configured.', true)
    return
  }
  if (selection?.kind !== 'opencode' && !showOpenCode()) return
  applyChatMode(!chatMode)
  // The messages box just changed size. Keep a reader who was pinned to the
  // bottom there; leave anyone reading older messages where they are.
  if (chatMode && openCodeStick) scrollOpenCodeLatest()
  else updateOpenCodeLatest()
}

/* ---------- dialogs ---------- */

function openDialog(id) {
  const dialog = dialogEl(id)
  debugLog('dialog', `open ${id} was=${dialog.open}`)
  if (!dialog.open) dialog.showModal()
  debugLog('dialog', `open ${id} now=${dialog.open}`)
  syncNativePaneVisibility()
}

function closeDialog(dialog) {
  debugLog('dialog', `close ${dialog.id} open=${dialog.open} via=closeDialog`)
  if (dialog?.open) dialog.close()
}

function openPairDialog() {
  $('pair-code').textContent = '—'
  inputEl('pair-url').value = ''
  inputEl('pair-label').value = 'Phone'
  selectEl('pair-scope').value = 'full'
  $('pair-note').textContent = ''
  $('pair-fingerprint').hidden = true
  $('pair-fingerprint').textContent = ''
  renderPairQR('')
  openDialog('dlg-pair')
  void refreshHostFingerprint()
  void refreshPairCode()
}

async function refreshHostFingerprint() {
  const label = $('pair-fingerprint')
  label.hidden = true
  label.textContent = ''
  try {
    const data = await api('/api/v1/e2ee/host-key')
    const lines = []
    if (data.fingerprintV2) {
      const suite = data.suite === 'x25519-mlkem768-v1'
        ? 'hybrid X25519 + ML-KEM-768'
        : (data.suite || '')
      lines.push(`Hybrid host key${suite ? ` (${suite})` : ''}: ${data.fingerprintV2}`)
    }
    if (data.fingerprint) {
      lines.push(`Legacy P-256 host key: ${data.fingerprint}`)
    }
    if (!lines.length) return
    // The phone shows one value depending on the suite it paired with; label
    // both so the user compares the line that matches the app's suite.
    label.style.whiteSpace = 'pre-line'
    label.textContent = lines.join('\n')
    label.hidden = false
  } catch (error) {
    debugLog('error', `host fingerprint: ${error.message}`)
  }
}

/** The native app handles this custom scheme without a camera dependency. */
function nativePairUri(webUrl, code) {
  if (!webUrl || !code) return ''
  try {
    const parsed = new URL(webUrl)
    if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') return ''
    return `darkfactory://pair?base=${encodeURIComponent(parsed.origin)}&code=${encodeURIComponent(code)}`
  } catch {
    return ''
  }
}

/** @param {string} url */
function renderPairQR(url) {
  const box = $('pair-qr')
  box.replaceChildren()
  box.hidden = true
  if (!url) return
  const QR = /** @type {any} */ (globalThis).QRCode
  if (typeof QR !== 'function') {
    debugLog('error', 'qrcode.js is not loaded')
    return
  }
  try {
    const qr = new QR(0, 0)
    qr.addData(url)
    qr.make()
    const count = qr.getModuleCount()
    box.style.setProperty('--qr-modules', String(count))
    const fragment = document.createDocumentFragment()
    for (let row = 0; row < count; row++) {
      for (let col = 0; col < count; col++) {
        const cell = document.createElement('i')
        if (qr.isDark(row, col)) cell.className = 'on'
        fragment.append(cell)
      }
    }
    box.append(fragment)
    box.hidden = false
  } catch (error) {
    debugLog('error', `qr: ${error.message}`)
  }
}

async function refreshPairCode() {
  const button = buttonEl('btn-pair-new')
  button.disabled = true
  $('pair-note').textContent = 'Generating a code…'
  renderPairQR('')
  try {
    const label = inputEl('pair-label').value.trim() || 'Phone'
    const scope = selectEl('pair-scope').value === 'read' ? 'read' : 'full'
    const data = await api('/api/v1/pair/code', { method: 'POST', body: { label, scope } })
    $('pair-code').textContent = data.code || '—'
    inputEl('pair-url').value = data.url || ''
    renderPairQR(nativePairUri(data.url || '', data.code || ''))
    let loopback = false
    try {
      const parsed = new URL(data.url)
      loopback = ['127.0.0.1', 'localhost', '::1', '[::1]'].includes(parsed.hostname)
    } catch { /* leave the warning off if the URL is unparseable */ }
    const expires = new Date(data.expiresAt)
    const seconds = Number.isFinite(expires.getTime())
      ? Math.max(0, Math.round((expires.getTime() - Date.now()) / 1000))
      : 0
    const scopeLabel = scope === 'read' ? 'read-only' : 'workspace access (terminal + chats)'
    $('pair-note').textContent = loopback
      ? `This link points at loopback. Replace the loopback host with your Tailscale or SSH host, or enter the code on the phone. The QR opens the native app; the link opens a local browser. ${scopeLabel}; expires in ${seconds}s.`
      : `${scopeLabel}; expires in ${seconds}s. The QR opens the native app; the link opens a local browser. The link and code work once.`
  } catch (error) {
    if (error.message !== 'unauthorized') $('pair-note').textContent = error.message
  } finally {
    button.disabled = false
  }
}

function openDevicesDialog() {
  $('devices-note').textContent = ''
  openDialog('dlg-devices')
  void refreshDevices()
}

async function refreshDevices() {
  const list = $('devices-list')
  list.replaceChildren(el('p', 'rows-empty', 'Loading…'))
  try {
    const data = await api('/api/v1/devices')
    renderDevices(Array.isArray(data?.devices) ? data.devices : [])
  } catch (error) {
    if (error.message === 'unauthorized') return
    list.replaceChildren(el('p', 'rows-empty', `Could not load devices: ${error.message}`))
  }
}

/** @param {Device[]} devices */
function renderDevices(devices) {
  const list = $('devices-list')
  list.replaceChildren()
  if (!devices.length) {
    list.append(el('p', 'rows-empty', 'No paired devices. This browser session is not a device record.'))
    return
  }
  for (const device of devices) {
    const row = el('div', 'device-row')
    const info = el('div', 'device-info')
    const title = el('div', 'device-title')
    title.append(el('span', '', device.label || 'Browser'))
    if (device.current) title.append(el('span', 'pill ok', 'this device'))
    title.append(el('span', `pill ${device.scope === 'read' ? 'warn' : 'ok'}`, device.scope === 'read' ? 'read-only' : 'workspace'))
    info.append(title)
    const seen = device.lastSeenAt ? new Date(device.lastSeenAt) : null
    const expires = device.expiresAt ? new Date(device.expiresAt) : null
    info.append(el('div', 'device-meta',
      `${seen && Number.isFinite(seen.getTime()) ? `last seen ${timeAgoMs(seen.getTime()) || 'now'}` : 'last seen unknown'}` +
      `${expires && Number.isFinite(expires.getTime()) ? ` · expires ${timeShort(expires.toISOString())}` : ''}`))
    const revoke = el('button', 'btn danger', device.current ? 'Revoke this device' : 'Revoke')
    revoke.type = 'button'
    revoke.dataset.deviceRevoke = device.id
    revoke.dataset.deviceLabel = device.label || 'Browser'
    revoke.dataset.deviceCurrent = device.current ? '1' : ''
    revoke.disabled = readOnly()
    row.append(info, revoke)
    list.append(row)
  }
}

async function revokeDevice(id, label, current) {
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  const ok = await confirmAction({
    title: `Revoke ${label}?`,
    text: current
      ? 'This device is this browser. Revoking it disconnects this tab and requires pairing again.'
      : 'The next request from that device is refused. The device must pair again to reconnect.',
    ok: 'Revoke device'
  })
  if (!ok) return
  try {
    await api(`/api/v1/devices/${encodeURIComponent(id)}`, { method: 'DELETE' })
    if (current) {
      lostAuth()
      return
    }
    await refreshDevices()
    toast('Device revoked.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Revoke failed: ${error.message}`, true)
  }
}

async function revokeAllOtherDevices() {
  if (readOnly()) {
    toast('This device is read-only.', true)
    return
  }
  const ok = await confirmAction({
    title: 'Revoke every other device?',
    text: 'All paired devices except this browser are signed out on their next request. They must pair again to reconnect.',
    ok: 'Revoke others'
  })
  if (!ok) return
  try {
    const result = await api('/api/v1/devices/revoke-all', { method: 'POST', body: { keepCurrent: true } })
    await refreshDevices()
    toast(`Revoked ${result?.revoked || 0} device${result?.revoked === 1 ? '' : 's'}.`)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Revoke failed: ${error.message}`, true)
  }
}

function confirmAction({ title, text, ok }) {
  const dialog = dialogEl('dlg-confirm')
  $('confirm-title').textContent = title
  $('confirm-text').textContent = text
  $('confirm-ok').textContent = ok || 'Confirm'
  dialog.returnValue = ''
  dialog.showModal()
  debugLog('dialog', `confirm open ${title}`)
  syncNativePaneVisibility()
  requestAnimationFrame(() => debugOpenCodeBox('confirmok', $('confirm-ok')))
  return new Promise((resolve) => { confirmResolve = resolve })
}

function fillRepoSelect(select, preferred) {
  select.replaceChildren()
  for (const repo of state.repos) {
    const option = el('option', '', `${repo.name} — ${repo.path}`)
    option.value = repo.id
    select.append(option)
  }
  if (preferred && state.repos.some((repo) => repo.id === preferred)) select.value = preferred
}

function fillWorktreeSelect(select, preferred) {
  select.replaceChildren()
  for (const wt of state.worktrees) {
    const repo = repoFor(wt.repoId)
    const option = el('option', '', `${repo ? repo.name : 'repo'} / ${wtName(wt)}`)
    option.value = wt.id
    select.append(option)
  }
  if (preferred && state.worktrees.some((wt) => wt.id === preferred)) select.value = preferred
}

/**
 * Fill the session dialog's location picker for one repo. Existing worktrees
 * and the in-place checkout are explicit choices; a new worktree is created
 * only when the user asks for one.
 * @param {string} repoId
 * @param {string} [preferredWorktreeId]
 */
function fillSessionWhere(repoId, preferredWorktreeId) {
  const select = selectEl('sess-where')
  select.replaceChildren()
  const repo = repoFor(repoId)
  if (!repo) return
  const worktrees = state.worktrees.filter((wt) => wt.repoId === repoId)
  const workspace = worktrees.find((wt) => wt.workspace)
  if (workspace) {
    const option = el('option', '', `${repo.git ? 'This checkout' : 'This folder'} — ${workspace.path}`)
    option.value = `worktree:${workspace.id}`
    select.append(option)
  } else {
    const option = el('option', '', `${repo.git ? 'This checkout (in place)' : 'This folder (in place)'} — ${repo.path}`)
    option.value = 'workspace'
    select.append(option)
  }
  for (const wt of worktrees) {
    if (wt.workspace) continue
    const option = el('option', '', `Worktree / ${wtName(wt)} — ${wt.path}`)
    option.value = `worktree:${wt.id}`
    select.append(option)
  }
  if (repo.git) {
    const option = el('option', '', 'New isolated worktree…')
    option.value = 'new'
    select.append(option)
  }
  const preferred = preferredWorktreeId ? `worktree:${preferredWorktreeId}` : ''
  if (preferred && Array.from(select.options).some((option) => option.value === preferred)) {
    select.value = preferred
  } else if (select.options.length) {
    select.selectedIndex = 0
  }
  updateSessionWhere()
}

function updateSessionWhere() {
  const repo = repoFor(selectEl('sess-repo').value)
  const value = selectEl('sess-where').value
  const fields = $('sess-new-fields')
  const name = inputIn('form-session', 'input[name="worktreeName"]')
  const hint = $('sess-hint')
  const isNew = value === 'new'
  fields.hidden = !isNew
  if (name) name.required = isNew
  if (isNew) {
    hint.textContent = 'Creates a new directory with its own branch under the daemon data root. Agent history starts fresh there.'
    return
  }
  if (value === 'workspace') {
    hint.textContent = repo?.git
      ? `Runs in ${repo.path}. This is your checkout, so agent history is shared. No branch or isolation.`
      : `Runs in ${repo?.path || 'this folder'}. No branch or isolation.`
    return
  }
  const worktreeId = value.startsWith('worktree:') ? value.slice('worktree:'.length) : ''
  const wt = worktreeFor(worktreeId)
  if (!wt) {
    hint.textContent = ''
    return
  }
  hint.textContent = wt.workspace
    ? `Runs in ${wt.path}. This is the checkout, so agent history is shared.`
    : `Runs in ${wt.path}. A worktree is a separate directory, so agent history from the checkout does not carry over.`
}

/**
 * A session started from a workspace/worktree row is already scoped to that
 * location. Hide the repo/location picker so the flow is workspace → agent.
 * @param {string} worktreeId
 */
function setSessionTarget(worktreeId) {
  const target = worktreeId ? worktreeFor(worktreeId) : null
  sessionTargetWorktreeId = target?.id || ''
  const targetMode = Boolean(target)
  $('sess-repo-field').hidden = targetMode
  $('sess-where-field').hidden = targetMode
  selectEl('sess-repo').required = !targetMode
  selectEl('sess-where').required = !targetMode
  $('sess-target').hidden = !targetMode
  $('sess-hint').hidden = targetMode
  $('sess-new-fields').hidden = true
  if (!target) {
    $('sess-target').textContent = ''
    return
  }
  const repo = repoFor(target.repoId)
  const where = target.workspace ? 'checkout · shares agent history' : 'isolated worktree · separate history'
  $('sess-target').textContent = `${repo ? repo.name : 'repo'} / ${wtName(target)} — ${target.path} · ${where}`
}

function fillAgentSelect(select) {
  select.replaceChildren()
  const shell = el('option', '', 'shell — $SHELL -l')
  shell.value = 'shell'
  select.append(shell)
  const names = Object.keys(state.agents || {}).sort()
  for (const name of names) {
    const preset = state.agents[name]
    const option = el('option', '', `${name} — ${preset.command}`)
    option.value = name
    select.append(option)
  }
  select.value = 'shell'
}

/** @param {string} path @param {string} home @returns {string} */
function shortPath(path, home) {
  if (!home) return path
  if (path === home) return '~'
  if (path.startsWith(home + '/')) return `~${path.slice(home.length)}`
  return path
}

function hideRepoBrowser() {
  debugLog('browse', `hide (was hidden=${$('repo-browser').hidden}) caller=${caller()}`)
  $('repo-browser').hidden = true
  $('btn-browse').setAttribute('aria-expanded', 'false')
}

function openRepoDialog() {
  debugLog('dialog', `openRepoDialog caller=${caller()}`)
  hideRepoBrowser()
  browserSeq++
  browserState = null
  $('browser-list').replaceChildren()
  $('browser-note').textContent = ''
  $('browser-note').classList.remove('bad', 'ok')
  buttonEl('browser-use').disabled = true
  openDialog('dlg-repo')
  buttonEl('btn-browse').focus()
}

/** @param {string} path @returns {Promise<void>} */
async function loadDirs(path) {
  const seq = ++browserSeq
  debugLog('browse', `load seq=${seq} path=${path || '(home)'}`)
  const list = $('browser-list')
  const note = $('browser-note')
  const use = buttonEl('browser-use')
  list.replaceChildren(el('div', 'browser-empty', 'Loading…'))
  note.textContent = ''
  note.classList.remove('bad', 'ok')
  use.disabled = true
  try {
    const suffix = path ? `?path=${encodeURIComponent(path)}` : ''
    const next = /** @type {DirsResponse} */ (await api(`/api/v1/fs/dirs${suffix}`))
    if (seq !== browserSeq) {
      debugLog('browse', `drop stale seq=${seq} current=${browserSeq}`)
      return
    }
    debugLog('browse', `ok seq=${seq} path=${next.path} entries=${next.entries.length} git=${next.git}`)
    browserState = next
    const input = inputIn('form-repo', 'input[name="path"]')
    if (input) {
      input.value = browserState.path
      input.title = browserState.path
    }
    use.disabled = false
    renderBrowser()
    logBrowserLayout(`rendered seq=${seq}`)
    window.setTimeout(() => logBrowserLayout(`rendered+250 seq=${seq}`), 250)
  } catch (error) {
    if (seq !== browserSeq) {
      debugLog('browse', `drop stale error seq=${seq} current=${browserSeq}`)
      return
    }
    debugLog('error', `browse seq=${seq} path=${path || '(home)'}: ${error.message}`)
    browserState = null
    $('browser-path').textContent = path || '—'
    $('browser-path').title = path || ''
    list.replaceChildren(el('div', 'browser-empty', 'Cannot open that folder.'))
    note.textContent = error.message
    note.classList.add('bad')
    note.classList.remove('ok')
    use.disabled = true
    buttonEl('browser-up').disabled = true
    buttonEl('browser-home').disabled = false
  }
}

function renderBrowser() {
  if (!browserState) return
  const data = browserState
  $('browser-path').textContent = shortPath(data.path, data.home)
  $('browser-path').title = data.path
  buttonEl('browser-up').disabled = !data.parent

  const showHidden = inputEl('browser-hidden').checked
  const entries = data.entries.filter((entry) => showHidden || !entry.name.startsWith('.'))
  const list = $('browser-list')
  list.replaceChildren()
  if (!entries.length) {
    list.append(el('div', 'browser-empty', data.entries.length ? 'Only hidden folders here.' : 'No subfolders.'))
  } else {
    for (const entry of entries) {
      const row = el('button', 'dir-row')
      row.type = 'button'
      row.title = entry.path
      row.dataset.dir = entry.path
      row.append(el('span', 'dir-mark', '▸'))
      row.append(el('span', 'dir-name', entry.name))
      if (entry.git) row.append(pill('git', 'ok'))
      row.append(el('span', 'dir-arrow', '›'))
      list.append(row)
    }
  }

  const note = $('browser-note')
  note.classList.toggle('bad', false)
  note.classList.toggle('ok', data.git)
  let text = data.git ? 'git repository' : 'not a git repository — opens as an in-place workspace'
  const hiddenCount = data.entries.filter((entry) => entry.name.startsWith('.')).length
  if (!showHidden && hiddenCount) text += ` · ${hiddenCount} hidden`
  if (data.truncated) text += ' · first 400 folders'
  note.textContent = text
}

/** @param {string} path */
function applyPickedFolder(path) {
  const input = inputIn('form-repo', 'input[name="path"]')
  if (input) {
    input.value = path
    input.title = path
  }
  const name = inputIn('form-repo', 'input[name="name"]')
  if (name && !name.value.trim()) {
    const base = path.split('/').filter(Boolean).pop()
    if (base) name.value = base
  }
  hideRepoBrowser()
  debugLog('browse', `native picked ${path}`)
  void api(`/api/v1/fs/dirs?path=${encodeURIComponent(path)}`).then((dirs) => {
    if (!dirs.git) toast('Not a git repository; it will open as an in-place workspace.')
  }).catch(() => { /* the path is still usable */ })
  const submit = /** @type {HTMLButtonElement|null} */ (formEl('form-repo').querySelector('button[type="submit"]'))
  if (submit) submit.focus()
}

async function browseRepo() {
  const path = inputIn('form-repo', 'input[name="path"]')
  const start = path ? path.value.trim() : ''
  const button = buttonEl('btn-browse')
  const label = button.textContent || 'Browse…'
  button.disabled = true
  button.textContent = 'Opening…'
  debugLog('browse', `native pick start=${start || '(empty)'}`)
  try {
    const picked = await api('/api/v1/fs/pick', { method: 'POST', body: { start } })
    if (picked && picked.path) {
      applyPickedFolder(picked.path)
      return
    }
    debugLog('browse', 'native pick cancelled')
  } catch (error) {
    if (error.message === 'unauthorized') return
    debugLog('browse', `native pick unavailable: ${error.message}; falling back`)
    await openRepoBrowser()
  } finally {
    button.disabled = false
    button.textContent = label
  }
}

async function openRepoBrowser() {
  const path = inputIn('form-repo', 'input[name="path"]')
  const wanted = path ? path.value.trim() : ''
  const panel = $('repo-browser')
  if (!panel.hidden && browserState && wanted === browserState.path) {
    debugLog('browse', 'already open; scroll into view (no reload)')
    panel.scrollIntoView({ block: 'nearest' })
    return
  }
  debugLog('browse', `open path=${wanted || '(empty)'}`)
  panel.hidden = false
  $('btn-browse').setAttribute('aria-expanded', 'true')
  logBrowserLayout('opened')
  await loadDirs(wanted)
}

function useFolder() {
  debugLog('browse', `useFolder state=${browserState ? browserState.path : 'null'}`)
  if (!browserState) return
  const path = inputIn('form-repo', 'input[name="path"]')
  if (path) path.value = browserState.path
  const name = inputIn('form-repo', 'input[name="name"]')
  if (name && !name.value.trim()) {
    const base = browserState.path.split('/').filter(Boolean).pop()
    if (base) name.value = base
  }
  const git = browserState.git
  hideRepoBrowser()
  if (!git) toast('Not a git repository; it will open as an in-place workspace.')
  const submit = /** @type {HTMLButtonElement|null} */ (formEl('form-repo').querySelector('button[type="submit"]'))
  if (submit) submit.focus()
}

/** @param {Repo} repo */
async function openWorkspace(repo) {
  try {
    const wt = await api('/api/v1/worktrees', {
      method: 'POST',
      body: { repoId: repo.id, workspace: true }
    })
    await refresh()
    selectWorktree(wt.id)
    toast(`Workspace ${repo.name} opened.`)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

/** @param {string} [repoId] */
function openWorktreeDialog(repoId) {
  if (!state.repos.length) {
    toast('Add a repo first.', true)
    openRepoDialog()
    return
  }
  const select = selectEl('wt-repo')
  const preferred = repoId || (selection?.kind === 'worktree'
    ? worktreeFor(selection.id)?.repoId
    : currentSession ? worktreeFor(currentSession.worktreeId)?.repoId : null)
  const preferredRepo = preferred ? repoFor(preferred) : null
  if (preferredRepo && !preferredRepo.git) {
    const existing = state.worktrees.find((wt) => wt.repoId === preferredRepo.id && wt.workspace)
    if (existing) {
      selectWorktree(existing.id)
    } else {
      void openWorkspace(preferredRepo)
    }
    return
  }
  fillRepoSelect(select, preferred)
  const name = inputIn('form-worktree', 'input[name="name"]')
  if (name) name.value = ''
  openDialog('dlg-worktree')
}

function openSessionDialog(worktreeId) {
  if (!hasTerminal()) {
    toast('This device is read-only.', true)
    return
  }
  if (!state.repos.length) {
    toast('Add a repo first.', true)
    openRepoDialog()
    return
  }
  const target = worktreeId ? worktreeFor(worktreeId) : null
  if (target) {
    setSessionTarget(target.id)
    fillAgentSelect(selectEl('sess-agent'))
  } else {
    const selectedWt = selection?.kind === 'worktree' ? worktreeFor(selection.id) : null
    const currentWt = currentSession ? worktreeFor(currentSession.worktreeId) : null
    const contextWt = selectedWt || currentWt
    const preferredRepoId = contextWt?.repoId || state.repos[0].id
    // A top-level "New session" starts from the checkout by default.
    setSessionTarget('')
    fillRepoSelect(selectEl('sess-repo'), preferredRepoId)
    fillSessionWhere(selectEl('sess-repo').value)
    fillAgentSelect(selectEl('sess-agent'))
  }
  const title = inputIn('form-session', 'input[name="title"]')
  if (title) title.value = ''
  sessionRequestKey = ''
  openDialog('dlg-session')
}

const RUN_EXAMPLE = JSON.stringify([
  { kind: 'command', name: 'test', command: ['go', 'test', './...'] }
], null, 2)

function openRunDialog() {
  if (!state.worktrees.length) {
    toast('Create a worktree or open a workspace first.', true)
    openWorktreeDialog()
    return
  }
  const preferred = scopedWorktreeId() || state.worktrees[state.worktrees.length - 1].id
  fillWorktreeSelect(selectEl('run-wt'), preferred)
  inputEl('run-steps').value = RUN_EXAMPLE
  inputIn('form-run', 'input[name="goal"]')?.focus()
  openDialog('dlg-run')
}

async function startRun(id) {
  try {
    await api(`/api/v1/runs/${encodeURIComponent(id)}/start`, { method: 'POST' })
    await refresh()
    await loadRunDetail(id)
    toast('Run started.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

async function cancelRun(id) {
  const ok = await confirmAction({
    title: 'Cancel this run?',
    text: 'The current step is stopped. Completed steps keep their records; the run becomes cancelled.',
    ok: 'Cancel run'
  })
  if (!ok) return
  try {
    await api(`/api/v1/runs/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
    await refresh()
    await loadRunDetail(id)
    toast('Run cancelled.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

async function answerRunStep(id, ordinal, result) {
  try {
    await api(`/api/v1/runs/${encodeURIComponent(id)}/steps/${ordinal}/answer`, {
      method: 'POST',
      body: { result }
    })
    await refresh()
    await loadRunDetail(id)
    toast(result === 'succeeded' ? 'Step marked done.' : result === 'failed' ? 'Step failed.' : 'Step skipped.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

/* ---------- mutations ---------- */

async function removeWorktree(wt) {
  const branch = wt.branch || 'the branch'
  const ok = await confirmAction({
    title: `Remove ${wtName(wt)}?`,
    text: `Deletes the worktree directory. ${branch} keeps whatever was committed.`,
    ok: 'Remove worktree'
  })
  if (!ok) return
  let forced = false
  try {
    await api(`/api/v1/worktrees/${encodeURIComponent(wt.id)}`, { method: 'DELETE' })
  } catch (error) {
    if (error.message === 'unauthorized') return
    // The daemon refuses to stop sessions or throw away files on the first ask.
    // Its message names what is at stake in parentheses; repeat the question
    // with that, and only then send the force.
    if (!error.message.includes('force=1')) {
      toast(error.message, true)
      return
    }
    const atRisk = /\(([^)]+)\)/.exec(error.message)?.[1] || ''
    const confirmed = await confirmAction({
      title: `Remove ${wtName(wt)} anyway?`,
      text: atRisk
        ? `${atRisk}. Live sessions are stopped and uncommitted files are deleted permanently; ${branch} keeps only what was committed.`
        : `${error.message}`,
      ok: 'Remove anyway'
    })
    if (!confirmed) return
    try {
      await api(`/api/v1/worktrees/${encodeURIComponent(wt.id)}?force=1`, { method: 'DELETE' })
      forced = true
    } catch (second) {
      if (second.message !== 'unauthorized') toast(second.message, true)
      return
    }
  }
  stopStream()
  currentSession = null
  selection = null
  await refresh()
  renderView()
  toast(forced ? 'Worktree removed; sessions stopped and uncommitted changes discarded.' : 'Worktree removed.')
}

async function removeRepo(repo) {
  const worktrees = state.worktrees.filter((wt) => wt.repoId === repo.id)
  const extraWorktrees = worktrees.filter((wt) => !wt.workspace)
  if (extraWorktrees.length) {
    toast(`Remove this repo's ${extraWorktrees.length} git worktree${extraWorktrees.length === 1 ? '' : 's'} first.`, true)
    return
  }
  const workspace = worktrees.some((wt) => wt.workspace)
  const ok = await confirmAction({
    title: `Remove repo ${repo.name}?`,
    text: workspace
      ? 'Stops live sessions and removes the workspace record. The directory and its files stay on disk.'
      : 'Only the daemon record is removed. The directory and its files stay on disk.',
    ok: 'Remove repo'
  })
  if (!ok) return
  try {
    await api(`/api/v1/repos/${encodeURIComponent(repo.id)}`, { method: 'DELETE' })
    await refresh()
    toast('Repo removed.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

/** @param {Session} sess */
async function removeSession(sess) {
  if (!sess) return
  const live = sess.live
  const ok = await confirmAction({
    title: live ? 'Stop this session?' : 'Remove this session?',
    text: live
      ? 'The PTY is killed and the session row is deleted. Files in the worktree are untouched.'
      : 'The daemon has no live PTY for this session. This removes the saved row; it cannot signal an old process. Files are untouched.',
    ok: live ? 'Stop session' : 'Remove session'
  })
  if (!ok) return
  const wasCurrent = currentSession?.id === sess.id
  const worktreeId = sess.worktreeId
  try {
    const result = await api(`/api/v1/sessions/${encodeURIComponent(sess.id)}`, { method: 'DELETE' })
    const verdict = result?.verdict || (live ? 'stopped' : 'unverifiable')
    if (wasCurrent) {
      stopStream()
      currentSession = null
      terminalSessionId = ''
      resetTerminal()
      if (terminal) terminal.options.disableStdin = true
      selection = worktreeFor(worktreeId) ? { kind: 'worktree', id: worktreeId } : null
    }
    await refresh()
    if (wasCurrent) {
      const remaining = sessionsFor(worktreeId)
      const next = remaining[remaining.length - 1]
      if (next) void openSession(next)
    }
    if (verdict === 'stopped') toast('Session stopped; process group verified gone.')
    else if (verdict === 'already_exited') toast('Session had already exited; row removed.')
    else toast('Session row removed; the old process could not be verified.', true)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  }
}

async function restartSession(sess) {
  if (!sess || sess.live || restarting.has(sess.id)) return
  const agent = sess.agent || (sess.kind === 'shell' ? 'shell' : '')
  if (!agent) {
    toast('This session has no agent preset to restart.', true)
    return
  }
  const dims = fitAddon ? fitAddon.proposeDimensions() : null
  const cols = dims && dims.cols >= 20 ? dims.cols : (sess.cols || 120)
  const rows = dims && dims.rows >= 5 ? dims.rows : (sess.rows || 32)
  restarting.add(sess.id)
  try {
    const next = await api('/api/v1/sessions', {
      method: 'POST',
      body: {
        worktreeId: sess.worktreeId,
        agent,
        kind: sess.kind === 'shell' ? 'shell' : 'agent',
        title: sess.title || '',
        cols,
        rows
      }
    })
    let removedOld = false
    try {
      await api(`/api/v1/sessions/${encodeURIComponent(sess.id)}`, { method: 'DELETE' })
      removedOld = true
    } catch { /* the new PTY is running; leave the tombstone if it cannot be removed */ }
    await refresh()
    expandedWorktrees.add(next.worktreeId)
    void openSession(next)
    toast(removedOld
      ? `Restarted ${agentLabel(next)}. New PTY; previous output was not persisted.`
      : `Restarted ${agentLabel(next)}. New PTY; the old row could not be removed.`)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  } finally {
    restarting.delete(sess.id)
  }
}

async function stopSession() {
  if (currentSession) await removeSession(currentSession)
}

/* ---------- wiring ---------- */

formEl('connect-form').addEventListener('submit', async (event) => {
  event.preventDefault()
  if (connectBusy) return
  connectBusy = true
  const form = eventForm(event)
  setBusy(form, true)
  $('connect-error').textContent = ''
  const value = inputEl('token').value
  const ok = await connect(value)
  setBusy(form, false)
  connectBusy = false
  if (ok) {
    if (currentSession && terminal) terminal.focus()
  } else {
    inputEl('token').value = value
    inputEl('token').select()
  }
})

buttonEl('btn-pair-connect').addEventListener('click', () => { void pairFromConnect() })
inputEl('pair-input').addEventListener('keydown', (event) => {
  if (event.key !== 'Enter') return
  event.preventDefault()
  void pairFromConnect()
})
$('btn-pair').addEventListener('click', openPairDialog)
$('btn-pair-new').addEventListener('click', () => { void refreshPairCode() })
$('btn-devices').addEventListener('click', openDevicesDialog)
$('btn-devices-refresh').addEventListener('click', () => { void refreshDevices() })
$('btn-devices-revoke-all').addEventListener('click', () => { void revokeAllOtherDevices() })
inputEl('pair-label').addEventListener('change', () => { void refreshPairCode() })
selectEl('pair-scope').addEventListener('change', () => { void refreshPairCode() })
$('devices-list').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-device-revoke]'))
  if (button?.dataset.deviceRevoke) {
    void revokeDevice(button.dataset.deviceRevoke, button.dataset.deviceLabel || 'device', button.dataset.deviceCurrent === '1')
  }
})
$('btn-pair-copy-code').addEventListener('click', () => { void copyText($('pair-code').textContent, 'Pairing code') })
$('btn-pair-copy-url').addEventListener('click', () => { void copyText(inputEl('pair-url').value, 'Pairing link') })
inputEl('pair-url').addEventListener('input', () => { renderPairQR(inputEl('pair-url').value.trim()) })
$('btn-disconnect').addEventListener('click', () => { void disconnect() })
$('btn-client-reload').addEventListener('click', () => { location.reload() })
$('btn-home').addEventListener('click', showFleet)
$('conn-pill').addEventListener('click', () => { void reconnect() })
$('btn-term-scroll-up').addEventListener('click', () => scrollTerminal(-1))
$('btn-term-scroll-down').addEventListener('click', () => scrollTerminal(1))
$('btn-term-latest').addEventListener('click', () => scrollTerminalLatest())
$('btn-term-font-down').addEventListener('click', () => adjustTermFont(-1))
$('btn-term-font-up').addEventListener('click', () => adjustTermFont(1))
const nativeLaunchKeys = new Map()
$('btn-term-native').addEventListener('click', () => {
  if (!currentSession?.live || readOnly()) return
  const id = currentSession.id
  buttonEl('btn-term-native').disabled = true
  if (!nativeLaunchKeys.has(id)) nativeLaunchKeys.set(id, newRequestKey('native'))
  void api(`/api/v1/sessions/${encodeURIComponent(id)}/native`, { method: 'POST', headers: { 'Idempotency-Key': nativeLaunchKeys.get(id) } })
    .then(() => nativeLaunchKeys.delete(id))
    .catch((error) => toast(`Ghostty: ${error.message}`, true))
    .finally(() => renderSession())
})
$('btn-term-focus').addEventListener('click', toggleZen)
/* The touch input row. A phone keyboard cannot send Escape, Tab or Ctrl, and
   xterm's hidden textarea is not a chat box, so the line path is a plain
   textarea that iOS owns. Enter sends, Shift+Enter keeps the newline. */
const termInput = inputEl('term-input')
const termCompose = $('form-term-input')
function sendTermInput() {
  queueInput(`${termInput.value}\r`)
  termInput.value = ''
  if (document.activeElement === termInput) termInput.focus()
}
termCompose.addEventListener('submit', (event) => {
  event.preventDefault()
  sendTermInput()
})
termInput.addEventListener('keydown', (event) => {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  sendTermInput()
})
for (const key of document.querySelectorAll('[data-term-key]')) {
  key.addEventListener('click', () => {
    const name = /** @type {HTMLElement} */ (key).dataset.termKey
    if (name === 'esc') queueInput('\x1b')
    else if (name === 'tab') queueInput('\t')
    else if (name === 'ctrl-c') queueInput('\x03')
  })
}
$('btn-runs').addEventListener('click', showRuns)
$('btn-wt-chat').addEventListener('click', () => {
  if (selection?.kind === 'worktree') showOpenCode(selection.id)
})
$('btn-sess-chat').addEventListener('click', () => {
  if (currentSession) showOpenCode(currentSession.worktreeId)
})
$('btn-blocked-chat').addEventListener('click', () => {
  if (currentSession) showOpenCode(currentSession.worktreeId)
})
$('btn-term-chat').addEventListener('click', () => {
  if (currentSession) showOpenCode(currentSession.worktreeId)
})
$('btn-oc-worktree').addEventListener('click', () => {
  if (openCodeScopeWorktreeId) selectWorktree(openCodeScopeWorktreeId, false)
})
$('btn-oc-new').addEventListener('click', openOpenCodeNewDialog)
$('btn-oc-new-side').addEventListener('click', openOpenCodeNewDialog)
$('btn-oc-refresh').addEventListener('click', () => { void refreshOpenCode({ sessions: true }) })
$('btn-oc-latest').addEventListener('click', () => scrollOpenCodeLatest())
$('btn-oc-mode').addEventListener('click', toggleChatMode)
$('btn-oc-interrupt').addEventListener('click', () => { void interruptOpenCode() })
$('btn-oc-older').addEventListener('click', () => { void loadOlderOpenCodeMessages() })
$('oc-sessions').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-oc-session]'))
  if (button?.dataset.ocSession) selectOpenCodeSession(button.dataset.ocSession)
})
$('oc-permissions').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-oc-permission]'))
  if (button?.dataset.ocPermission && button.dataset.ocDecision) {
    void replyOpenCodePermission(button.dataset.ocPermission, button.dataset.ocDecision)
  }
})
inputEl('oc-search').addEventListener('input', () => {
  openCodeSearch = inputEl('oc-search').value.trim()
  if (openCodeSearchTimer) window.clearTimeout(openCodeSearchTimer)
  openCodeSearchTimer = window.setTimeout(() => {
    openCodeSearchTimer = 0
    void loadOpenCodeSessions()
  }, 300)
})
selectEl('oc-session-select').addEventListener('change', () => {
  const select = selectEl('oc-session-select')
  selectOpenCodeSession(select.value)
  // End the browser's picker explicitly. Leaving it focused let the popup
  // outlive the selection and swallow every later tap.
  select.blur()
})
// The picker popup keeps the select focused, so the deferred rebuild and the
// value write land here once it is gone.
selectEl('oc-session-select').addEventListener('focusout', () => {
  debugLog('oc', 'picker closed')
  renderOpenCodeSessions()
})
selectEl('oc-session-select').addEventListener('focus', () => {
  debugLog('oc', `picker open current=${shortId(openCodeSessionId)}`)
})
$('oc-messages').addEventListener('scroll', () => {
  if (openCodeJumping) return
  openCodeStick = openCodeNearBottom()
  updateOpenCodeLatest()
}, { passive: true })
let composeBlurTimer = 0
inputEl('oc-prompt').addEventListener('focus', () => {
  window.clearTimeout(composeBlurTimer)
  document.documentElement.dataset.compose = '1'
})
inputEl('oc-prompt').addEventListener('blur', () => {
  // A Send tap blurs the textarea before its click lands. Keep the compact
  // keyboard layout for one frame's worth of taps, or the DOM moves under the
  // finger and the tap only dismisses the keyboard.
  window.clearTimeout(composeBlurTimer)
  composeBlurTimer = window.setTimeout(() => {
    const form = document.getElementById('form-oc-prompt')
    if (!form || !form.contains(document.activeElement)) {
      delete document.documentElement.dataset.compose
    }
  }, 400)
})
inputEl('oc-prompt').addEventListener('input', () => {
  const input = inputEl('oc-prompt')
  input.style.height = 'auto'
  input.style.height = `${Math.min(input.scrollHeight, 160)}px`
  updateOpenCodeCommands()
})
$('oc-commands').addEventListener('click', (event) => {
  const item = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-command]'))
  if (item?.dataset.command) chooseOpenCodeCommand(item.dataset.command)
})
inputEl('oc-prompt').addEventListener('keydown', (event) => {
  if (handleOpenCodeCommandKeys(event)) return
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing || isMobile()) return
  event.preventDefault()
  void sendOpenCodePrompt()
})
$('btn-new-run').addEventListener('click', openRunDialog)
$('run-example').addEventListener('click', () => { inputEl('run-steps').value = RUN_EXAMPLE })
$('runs-list').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-run]'))
  if (!button?.dataset.run) return
  currentRunId = button.dataset.run
  runDetail = null
  renderRuns()
})
$('run-body').addEventListener('click', (event) => {
  const target = eventElement(event)
  const start = /** @type {HTMLElement|null} */ (target.closest('[data-run-start]'))
  if (start?.dataset.runStart) {
    void startRun(start.dataset.runStart)
    return
  }
  const cancel = /** @type {HTMLElement|null} */ (target.closest('[data-run-cancel]'))
  if (cancel?.dataset.runCancel) {
    void cancelRun(cancel.dataset.runCancel)
    return
  }
  const answer = /** @type {HTMLElement|null} */ (target.closest('[data-run-answer]'))
  if (answer?.dataset.runAnswer && answer.dataset.runResult && currentRunId) {
    void answerRunStep(currentRunId, Number(answer.dataset.runAnswer), answer.dataset.runResult)
    return
  }
  const session = /** @type {HTMLElement|null} */ (target.closest('[data-run-session]'))
  if (session?.dataset.runSession) {
    const sess = sessionFor(session.dataset.runSession)
    if (sess) void openSession(sess)
  }
})

detailsEl('new-menu').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-new]'))
  if (!button) return
  detailsEl('new-menu').open = false
  if (button.dataset.new === 'repo') openRepoDialog()
  if (button.dataset.new === 'worktree') openWorktreeDialog()
  if (button.dataset.new === 'session') {
    const target = selection?.kind === 'worktree' ? selection.id : currentSession?.worktreeId
    openSessionDialog(target)
  }
})

document.addEventListener('click', (event) => {
  const menu = detailsEl('new-menu')
  if (menu.open && event.target instanceof Node && !menu.contains(event.target)) menu.open = false
})

$('tree').addEventListener('click', (event) => {
  const kill = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-tree-session-kill]'))
  if (kill) {
    const sess = sessionFor(kill.dataset.treeSessionKill)
    if (sess) void removeSession(sess)
    return
  }
  const toggle = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-wt-toggle]'))
  if (toggle) {
    const id = toggle.dataset.wtToggle
    if (id) {
      if (expandedWorktrees.has(id)) expandedWorktrees.delete(id)
      else expandedWorktrees.add(id)
      renderTree()
    }
    return
  }
  const session = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-tree-session-open]'))
  if (session) {
    const sess = sessionFor(session.dataset.treeSessionOpen)
    if (sess) void openSession(sess)
    return
  }
  const remove = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-repo-del]'))
  if (remove) {
    const repo = repoFor(remove.dataset.repoDel)
    if (repo) void removeRepo(repo)
    return
  }
  const workspace = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-repo-workspace]'))
  if (workspace) {
    const repo = repoFor(workspace.dataset.repoWorkspace)
    if (repo) void openWorkspace(repo)
    return
  }
  const quick = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-wt-new-session]'))
  if (quick?.dataset.wtNewSession) {
    openSessionDialog(quick.dataset.wtNewSession)
    return
  }
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-wt]'))
  if (button) selectWorktree(button.dataset.wt)
})

$('tree').addEventListener('contextmenu', (event) => {
  const target = eventElement(event)
  const session = /** @type {HTMLElement|null} */ (target.closest('[data-tree-session]'))
  if (session?.dataset.treeSession) {
    showContextMenu(event, { kind: 'session', id: session.dataset.treeSession }, session)
    return
  }
  const wt = /** @type {HTMLElement|null} */ (target.closest('[data-wt]'))
  const toggle = /** @type {HTMLElement|null} */ (target.closest('[data-wt-toggle]'))
  const wtId = wt?.dataset.wt || toggle?.dataset.wtToggle
  if (wtId) showContextMenu(event, { kind: 'worktree', id: wtId }, wt || toggle)
})

/** @param {string} id */
function bindSessionContextMenu(id) {
  $(id).addEventListener('contextmenu', (event) => {
    const target = eventElement(event)
    const close = /** @type {HTMLElement|null} */ (target.closest('[data-session-close]'))
    const open = /** @type {HTMLElement|null} */ (target.closest('[data-session]'))
    const sessionId = close?.dataset.sessionClose || open?.dataset.session
    if (sessionId) showContextMenu(event, { kind: 'session', id: sessionId }, close || open)
  })
}
bindSessionContextMenu('wt-sessions')
bindSessionContextMenu('session-tabs')

$('ctx-menu').addEventListener('click', (event) => {
  const item = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-ctx]'))
  if (!item) return
  void runContextAction(item.dataset.ctx || '')
})

$('ctx-menu').addEventListener('keydown', (event) => {
  const menu = $('ctx-menu')
  if (event.key === 'Escape') {
    hideContextMenu()
    return
  }
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
  const items = /** @type {HTMLButtonElement[]} */ ([...menu.querySelectorAll('button:not(:disabled)')])
  if (!items.length) return
  const current = items.indexOf(/** @type {HTMLButtonElement} */ (document.activeElement))
  const next = event.key === 'ArrowDown'
    ? (current + 1) % items.length
    : (current - 1 + items.length) % items.length
  event.preventDefault()
  items[next].focus()
})

document.addEventListener('pointerdown', (event) => {
  const menu = $('ctx-menu')
  if (menu.hidden) return
  if (event.target instanceof Node && menu.contains(event.target)) return
  hideContextMenu()
}, true)
document.addEventListener('pointerdown', (event) => {
  if (!attentionOpen) return
  const pop = $('attention-pop')
  if (event.target instanceof Node && pop.contains(event.target)) return
  if (event.target instanceof Node && $('btn-attention').contains(event.target)) return
  closeAttentionPanel()
}, true)
document.addEventListener('keydown', (event) => {
  if (event.key !== 'Escape') return
  hideContextMenu()
  if (attentionOpen) closeAttentionPanel()
})
window.addEventListener('resize', () => {
  hideContextMenu()
  closeAttentionPanel()
})
document.addEventListener('scroll', hideContextMenu, true)

$('attention-list').addEventListener('click', (event) => {
  const target = eventElement(event)
  const dismiss = /** @type {HTMLElement|null} */ (target.closest('[data-attention-dismiss]'))
  if (dismiss) {
    dismissAttention(Number(dismiss.dataset.attentionDismiss) || 0)
    return
  }
  const go = /** @type {HTMLElement|null} */ (target.closest('[data-attention-go]'))
  if (!go) return
  const id = Number(go.dataset.attentionGo) || 0
  const found = attentionEvents.find((row) => row.id === id)
  if (found) followAttention(found)
})
$('attention-clear').addEventListener('click', clearAttention)
$('btn-alert-settings').addEventListener('click', () => { void openAlertSettings() })
$('form-alerts').addEventListener('submit', (event) => { void saveAlertSettings(event) })

$('session-tabs').addEventListener('click', (event) => {
  const close = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-session-close]'))
  if (close) {
    const sess = sessionFor(close.dataset.sessionClose)
    if (sess) void removeSession(sess)
    return
  }
  const add = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-session-add]'))
  if (add) {
    openSessionDialog(currentSession?.worktreeId)
    return
  }
  const tab = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-session]'))
  if (tab) {
    const sess = sessionFor(tab.dataset.session)
    if (sess && sess.id !== currentSession?.id) void openSession(sess)
  }
})

$('wt-sessions').addEventListener('click', (event) => {
  const button = /** @type {HTMLElement|null} */ (eventElement(event).closest('[data-session]'))
  if (!button) return
  const sess = sessionFor(button.dataset.session)
  if (sess) void openSession(sess)
})

$('btn-add-repo').addEventListener('click', openRepoDialog)
$('btn-attention').addEventListener('click', toggleAttentionPanel)
$('btn-alert-settings-top').addEventListener('click', () => { void openAlertSettings() })
$('btn-term-refresh').addEventListener('click', refreshTerminalNow)
$('btn-empty-repo').addEventListener('click', openRepoDialog)
$('btn-browse').addEventListener('click', () => { void browseRepo() })
buttonEl('browser-close').addEventListener('click', hideRepoBrowser)
buttonEl('browser-up').addEventListener('click', () => {
  if (browserState?.parent) void loadDirs(browserState.parent)
})
buttonEl('browser-home').addEventListener('click', () => {
  void loadDirs(browserState?.home || '')
})
buttonEl('browser-use').addEventListener('click', useFolder)
inputEl('browser-hidden').addEventListener('change', renderBrowser)
$('browser-list').addEventListener('click', (event) => {
  const row = eventElement(event).closest('[data-dir]')
  const path = row ? row.getAttribute('data-dir') : ''
  if (path) void loadDirs(path)
})
buttonEl('btn-empty-session').addEventListener('click', () => openSessionDialog())
selectEl('sess-repo').addEventListener('change', () => {
  fillSessionWhere(selectEl('sess-repo').value)
})
selectEl('sess-where').addEventListener('change', updateSessionWhere)
$('btn-new-session').addEventListener('click', () => openSessionDialog(currentSession?.worktreeId))
$('btn-wt-new-session').addEventListener('click', () => {
  openSessionDialog(selection?.kind === 'worktree' ? selection.id : undefined)
})
buttonEl('btn-stop-session').addEventListener('click', stopSession)
buttonEl('btn-restart-session').addEventListener('click', () => { void restartSession(currentSession) })
$('btn-wt-remove').addEventListener('click', () => {
  const wt = selection?.kind === 'worktree' ? worktreeFor(selection.id) : null
  if (!wt) return
  if (wt.workspace) {
    const repo = repoFor(wt.repoId)
    if (repo) void removeRepo(repo)
    return
  }
  void removeWorktree(wt)
})

formEl('form-repo').addEventListener('submit', async (event) => {
  event.preventDefault()
  debugLog('form', 'submit form-repo')
  const form = eventForm(event)
  if (isBusy(form)) return
  const data = new FormData(form)
  const path = String(data.get('path') || '').trim()
  const name = String(data.get('name') || '').trim()
  if (!path) return
  setBusy(form, true)
  try {
    const repo = await api('/api/v1/repos', { method: 'POST', body: { path, name } })
    hideRepoBrowser()
    browserState = null
    closeDialog(dialogEl('dlg-repo'))
    form.reset()
    await refresh()
    const workspace = state.worktrees.find((wt) => wt.repoId === repo.id && wt.workspace)
    if (workspace) selectWorktree(workspace.id)
    toast(`Repo ${repo.name} added with its workspace.`)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  } finally {
    setBusy(form, false)
  }
})

formEl('form-worktree').addEventListener('submit', async (event) => {
  event.preventDefault()
  debugLog('form', 'submit form-worktree')
  const form = eventForm(event)
  if (isBusy(form)) return
  const data = new FormData(form)
  const repoId = String(data.get('repoId') || '')
  const name = String(data.get('name') || '').trim()
  const baseRef = String(data.get('baseRef') || '').trim() || 'HEAD'
  if (!repoId || !name) return
  setBusy(form, true)
  try {
    const wt = await api('/api/v1/worktrees', { method: 'POST', body: { repoId, name, baseRef } })
    closeDialog(dialogEl('dlg-worktree'))
    form.reset()
    await refresh()
    selectWorktree(wt.id)
    toast(wt.workspace
      ? `Workspace ${wtName(wt)} opened.`
      : `Worktree ${wtName(wt)} created on ${wt.branch}.`)
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  } finally {
    setBusy(form, false)
  }
})

formEl('form-oc-prompt').addEventListener('submit', (event) => {
  event.preventDefault()
  void sendOpenCodePrompt()
})

formEl('form-oc-new').addEventListener('submit', async (event) => {
  event.preventDefault()
  const form = eventForm(event)
  if (isBusy(form)) return
  const data = new FormData(form)
  const worktreeId = String(data.get('worktreeId') || '')
  const title = String(data.get('title') || '').trim()
  const agent = String(data.get('agent') || '').trim()
  if (!worktreeId) return
  setBusy(form, true)
  try {
    const created = await api('/api/v1/opencode/sessions', {
      method: 'POST',
      body: { worktreeId, title, agent }
    })
    closeDialog(dialogEl('dlg-oc-new'))
    form.reset()
    const id = created?.data?.id || ''
    await loadOpenCodeSessions()
    if (id) {
      openCodeSessionId = id
      openCodeMessages = []
      openCodeMessageSignature = ''
      openCodeStick = true
      renderOpenCodeSessions()
      renderOpenCodeMessages()
      await loadOpenCodeMessages()
    }
    toast('OpenCode chat started.')
    if (!isMobile()) inputEl('oc-prompt').focus()
  } catch (error) {
    if (error.message !== 'unauthorized') toast(`Could not start chat: ${error.message}`, true)
  } finally {
    setBusy(form, false)
  }
})

formEl('form-run').addEventListener('submit', async (event) => {
  event.preventDefault()
  const form = eventForm(event)
  if (isBusy(form)) return
  const data = new FormData(form)
  const worktreeId = String(data.get('worktreeId') || '')
  const goal = String(data.get('goal') || '').trim()
  let steps
  try {
    steps = JSON.parse(String(data.get('steps') || '[]'))
  } catch (error) {
    toast(`Plan JSON: ${error.message}`, true)
    return
  }
  if (!worktreeId || !goal || !Array.isArray(steps) || !steps.length) {
    toast('Pick a worktree, write a goal, and add at least one step.', true)
    return
  }
  setBusy(form, true)
  try {
    const run = await api('/api/v1/runs', { method: 'POST', body: { worktreeId, goal, steps } })
    await api(`/api/v1/runs/${encodeURIComponent(run.id)}/start`, { method: 'POST' })
    closeDialog(dialogEl('dlg-run'))
    form.reset()
    currentRunId = run.id
    runDetail = null
    await refresh()
    showRuns()
    await loadRunDetail(run.id)
    toast('Run started.')
  } catch (error) {
    if (error.message !== 'unauthorized') toast(error.message, true)
  } finally {
    setBusy(form, false)
  }
})

formEl('form-session').addEventListener('submit', async (event) => {
  event.preventDefault()
  debugLog('form', 'submit form-session')
  const form = eventForm(event)
  if (isBusy(form)) return
  const data = new FormData(form)
  const repoId = String(data.get('repoId') || '')
  const where = String(data.get('location') || '')
  const agent = String(data.get('agent') || 'shell')
  const title = String(data.get('title') || '').trim()
  const worktreeName = String(data.get('worktreeName') || '').trim()
  const baseRef = String(data.get('baseRef') || '').trim() || 'HEAD'
  let worktreeId = sessionTargetWorktreeId
  if (!worktreeId) {
    if (!repoId || !where) return
    if (where === 'new' && !worktreeName) {
      toast('Name the new worktree.', true)
      return
    }
    if (where.startsWith('worktree:')) worktreeId = where.slice('worktree:'.length)
  }
  /** @type {Worktree|null} */
  let created = null
  const dims = fitAddon ? fitAddon.proposeDimensions() : null
  const cols = dims && dims.cols >= 20 ? dims.cols : 120
  const rows = dims && dims.rows >= 5 ? dims.rows : 32
  setBusy(form, true)
  try {
    if (!sessionTargetWorktreeId) {
      if (where === 'workspace') {
        const wt = await api('/api/v1/worktrees', {
          method: 'POST',
          body: { repoId, workspace: true }
        })
        created = wt
        worktreeId = wt.id
      } else if (where === 'new') {
        const wt = await api('/api/v1/worktrees', {
          method: 'POST',
          body: { repoId, name: worktreeName, baseRef }
        })
        created = wt
        worktreeId = wt.id
      }
    }
    if (!worktreeId) {
      toast('Pick where to run the session.', true)
      return
    }
    if (!sessionRequestKey) sessionRequestKey = newRequestKey('session')
    const sess = await api('/api/v1/sessions', {
      method: 'POST',
      headers: { 'Idempotency-Key': sessionRequestKey },
      body: {
        worktreeId,
        agent,
        kind: agent === 'shell' ? 'shell' : 'agent',
        title,
        cols,
        rows
      }
    })
    sessionRequestKey = ''
    closeDialog(dialogEl('dlg-session'))
    form.reset()
    expandedWorktrees.add(worktreeId)
    await refresh()
    void openSession(sess)
    toast(`Session ${agentLabel(sess)} started.`)
  } catch (error) {
    if (created) {
      await refresh()
      fillRepoSelect(selectEl('sess-repo'), created.repoId)
      fillSessionWhere(created.repoId, created.id)
      selectWorktree(created.id)
      if (error.message !== 'unauthorized') {
        const label = created.workspace ? 'Workspace' : 'Worktree'
        toast(`${label} created, but the session did not start: ${error.message}`, true)
      }
    } else if (error.message !== 'unauthorized') {
      toast(error.message, true)
    }
  } finally {
    setBusy(form, false)
  }
})

const closeButtons = /** @type {NodeListOf<HTMLElement>} */ (document.querySelectorAll('[data-close]'))
closeButtons.forEach((button) => {
  button.addEventListener('click', () => {
    const dialog = /** @type {HTMLDialogElement|null} */ (button.closest('dialog'))
    if (dialog) closeDialog(dialog)
  })
})

const allDialogs = /** @type {NodeListOf<HTMLDialogElement>} */ (document.querySelectorAll('dialog'))
allDialogs.forEach((dialog) => {
  dialog.addEventListener('close', () => {
    debugLog('dialog', `closed ${dialog.id} return=${dialog.returnValue || '-'}`)
    // Any way a modal ends — Cancel, Escape, a form method=dialog button —
    // brings the native pane back over the terminal it was hiding.
    syncNativePaneVisibility()
  })
  dialog.addEventListener('cancel', () => {
    debugLog('dialog', `cancel ${dialog.id} (Escape)`)
  })
})

dialogEl('dlg-confirm').addEventListener('close', () => {
  if (!confirmResolve) return
  const resolve = confirmResolve
  confirmResolve = null
  resolve(dialogEl('dlg-confirm').returnValue === 'ok')
})
$('confirm-ok').addEventListener('click', () => dialogEl('dlg-confirm').close('ok'))

const navButtons = /** @type {NodeListOf<HTMLElement>} */ (document.querySelectorAll('.nav-btn'))
navButtons.forEach((button) => {
  button.addEventListener('click', () => {
    const view = button.dataset.view
    if (view === 'fleet') showFleet()
    if (view === 'session') showSession()
    if (view === 'runs') showRuns()
  })
})

document.addEventListener('visibilitychange', () => {
  if (document.hidden || !authenticated) return
  void refresh()
  void pollNotifications()
  if (selection?.kind === 'opencode') {
    void refreshOpenCode()
    void startOpenCodeEvents()
  }
})

if (!window.isSecureContext) $('insecure-banner').hidden = false

const footerVersion = document.getElementById('ui-version')
if (footerVersion) footerVersion.textContent = `ui ${UI_VERSION}`
initDebug()
initTerminal()

async function bootAuth() {
  const pairCode = takePairCode()
  if (pairCode) {
    const ok = await pairDevice(pairCode)
    if (ok) return
    // The pairing card already explains the failure; do not overwrite it with
    // a cookie attempt that has no chance of succeeding.
    return
  }
  const code = takeOpenCode()
  if (code) {
    try {
      await api('/api/v1/open', { method: 'POST', body: { code } })
      await connect('')
      return
    } catch (error) {
      debugLog('error', `open exchange failed: ${error.message}`)
      $('connect-error').textContent = 'That one-time link is invalid or expired. Run darkfactory open again.'
    }
  }
  if (token) {
    if (await connect(token)) return
    // sessionStorage can outlive an in-memory session or a daemon restart. A
    // failed stored token is not the end if this device has a key.
    await deviceKeyAuth()
    return
  }
  // No pasted token: try the HttpOnly cookie, then a remembered device key.
  // WebKitGTK rejects the Secure __Host- cookie over loopback HTTP, so the key
  // is what makes a desktop launch survive without re-authentication.
  if (await connect('')) return
  await deviceKeyAuth()
}

setConn(token ? 'connecting' : 'offline')
void bootAuth()
