import { inject, nextTick, reactive, type InjectionKey } from 'vue'
import { api } from './api'
import { guidType, parseGuid } from '~/utils/format'

export type Tab = 'info' | 'map' | 'texture' | 'model' | 'material' | 'audio' | 'tree' | 'graph' | 'entity' | 'deps' | 'refs' | 'hex' | 'text'

// A document tab: one open resource with its own back/forward history and view state.
export interface DocTab {
  id: number
  history: string[]
  cursor: number
  tab: Tab
  instance: number
}

// A pane shows resources: its document tabs, and the active tab's resource and selection (current resource,
// view tab, STU instance / map record, tree path, graph node, history). The main window has the main pane and,
// in split view, a second one that keeps its resource while the main pane navigates.
function newPane() {
  return reactive({
    id: 0,
    current: '' as string,
    resource: null as any,
    error: '',
    tab: 'info' as Tab,
    instance: -1,        // selected STU instance (map view: record)
    path: '',            // selected tree path
    graphNode: -1,       // selected graph node position
    history: [] as string[],
    cursor: -1,
    docs: [] as DocTab[],
    active: 0,
    explicitTab: false,
    extra: {} as Record<string, any>, // per view tab: its own state for the MCP server (map mode / camera, texture mip, …)
  })
}
export type Pane = ReturnType<typeof newPane>
const PANE_KEYS = new Set(['current', 'resource', 'error', 'tab', 'instance', 'path', 'graphNode', 'history', 'cursor', 'docs', 'active', 'extra'])

// App-wide state (selection model for a future editor: the panes above + browsing state).
const g = reactive({
  status: null as any,
  types: [] as any[],
  packages: [] as any[],
  kinds: [] as any[], // reference index edge kinds
  ui: { pinned: [] as string[], recent: [] as string[], panels: {} as Record<string, number> },
  filter: { type: -1, package: '', search: '' },
  labels: {} as Record<string, string>, // guid → short label (breadcrumbs, tab titles)
  listVersion: 0,      // bumps when names change (index)
  rail: 'types' as 'types' | 'packages' | 'pinned' | 'recent',
  reveal: null as null | { guid: string; n: number }, // ResourceList scrolls to it
  menu: null as null | { x: number; y: number; guid: string; pane: Pane }, // GUID context menu
  toast: '',
  wide: false,  // hide the rail and the list (more room for the resource view)
  split: null as null | Pane, // the second pane (split view)
  focus: null as null | Pane, // the pane keys act on (last clicked)
  mapMax: 0, // map view maximised to the whole window: the pane's id (0 = none; MapView, T066)
  mapCursor: '', // map view: world coordinates under the cursor (status bar; not `cursor`, a pane's history index)
  mcp: null as any, // MCP server state (App.McpStatus: port, drive toggle, calls)
  driveTick: 0,     // bumped by an MCP drive command (views re-focus their selection)
  // a document tab being dragged (DocTabs): the pane and index it comes from, the pointer, the drop target
  docDrag: null as null | { from: Pane; index: number; label: string; x: number; y: number; over: null | { pane: number; index: number } },
  tabMenu: null as null | { x: number; y: number; pane: Pane; index: number }, // a document tab's context menu
})

export const mainPane = newPane()
mainPane.id = 1
g.focus = mainPane
export const PANE: InjectionKey<Pane> = Symbol('pane')

// view merges the app state and a pane into the `s` components use (s.tab, s.resource: the pane's; s.types: app).
const views = new WeakMap<Pane, any>()
function view(p: Pane): typeof g & Pane {
  let v = views.get(p)
  if (!v) {
    v = new Proxy(g, {
      get: (t, k) => (PANE_KEYS.has(k as string) ? (p as any)[k] : (t as any)[k]),
      set: (t, k, val) => { if (PANE_KEYS.has(k as string)) (p as any)[k] = val; else (t as any)[k] = val; return true },
    })
    views.set(p, v)
  }
  return v
}

let nextId = 1

function label(r: any): string {
  if (r?.name?.text) return r.name.text
  const x: string = r?.guid ?? ''
  return `${r?.typeLabel ?? ''} ${x.slice(0, 4)}…${x.slice(4).replace(/^0+(?=.)/, '')}`.trim()
}

async function load(p: Pane, guid: string, pushHistory: boolean) {
  p.current = guid
  p.error = ''
  p.instance = -1
  p.path = ''
  p.graphNode = -1
  if (pushHistory) {
    p.history = p.history.slice(0, p.cursor + 1)
    p.history.push(guid)
    p.cursor = p.history.length - 1
  }
  saveDoc(p)
  try {
    const r = await api.Resource(guid)
    if (p.current !== guid) return
    p.resource = r
    g.labels[guid] = label(r)
    const tabs = tabsFor(r)
    if (!p.explicitTab || !tabs.includes(p.tab)) p.tab = tabs[0]
    p.explicitTab = false
    saveDoc(p)
    g.ui = await api.Visit(guid)
  } catch (e: any) {
    p.resource = null
    p.error = String(e)
  }
}

// saveDoc copies the pane's live state into its active document tab.
function saveDoc(p: Pane) {
  let d = p.docs[p.active]
  if (!d) {
    d = { id: nextId++, history: [], cursor: -1, tab: p.tab, instance: -1 }
    p.docs.push(d)
    p.active = p.docs.length - 1
  }
  d.history = p.history.slice()
  d.cursor = p.cursor
  d.tab = p.tab
  d.instance = p.instance
}

function activate(p: Pane, i: number) {
  if (i < 0 || i >= p.docs.length) return
  if (p.docs[p.active]) saveDoc(p)
  p.active = i
  const d = p.docs[i]
  p.history = d.history.slice()
  p.cursor = d.cursor
  const guid = d.history[d.cursor]
  if (!guid) { p.current = ''; p.resource = null; return }
  p.tab = d.tab
  p.explicitTab = true
  const inst = d.instance
  load(p, guid, false).then(() => { p.instance = inst })
}

export const AUDIO_TYPES = [0x02c, 0x02d, 0x02e, 0x02f, 0x030, 0x03f, 0x043, 0x0b2]

export function tabsFor(r: any): Tab[] {
  if (!r) return []
  const t: Tab[] = []
  if (r.type === 0x02 || r.type === 0x9f) t.push('map') // map data, map header (docs/Explorer.md "Map view")
  if (r.kind === 'texture') t.push('texture') // texture 004, payload 04D (docs/Explorer.md "Texture view")
  if (r.kind === 'model' || r.type === 0x1a) t.push('model') // model 00C, look 01A (docs/Explorer.md "Model view")
  if (r.kind === 'material') t.push('material')
  // WEMs 03F / 0B2, sounds 02C, banks 043, switch / state groups, switches, parameters 02D–030 (docs/Explorer.md "Audio view")
  if (AUDIO_TYPES.includes(r.type)) t.push('audio')
  if (r.kind === 'stu') {
    if (r.type === 0x1b) t.push('graph')
    if (r.type === 0x03) t.push('entity')
    t.push('tree')
  } else if (r.kind === 'text') t.push('text')
  t.push('info', 'deps', 'refs', 'hex')
  return t
}

// newWindow: a second app window (process) on the resource; split: the split view's second pane.
export interface OpenOpts { tab?: Tab; instance?: number; newTab?: boolean; background?: boolean; newWindow?: boolean; split?: boolean }

function link(guid: string, o: OpenOpts) {
  let l = `ow://${guid}`
  if (o.tab) l += `/${o.tab}` + (o.instance !== undefined && o.instance >= 0 ? `/${o.instance}` : '')
  return l
}

function open(p: Pane, guid: string, opts: OpenOpts = {}) {
  const x = parseGuid(guid)
  if (!x) return
  if (opts.newWindow) {
    api.OpenWindow(link(x, opts)).then(() => flash('Opening a new window…')).catch((e: any) => flash(`New window failed: ${e}`))
    return
  }
  if (opts.split) {
    if (!g.split) { g.split = newPane(); g.split.id = 2 }
    open(g.split, x, { ...opts, split: false, newTab: g.split.docs.length > 0 && !!g.split.docs[g.split.active]?.history.length })
    g.focus = g.split
    return
  }
  const cur = p.docs[p.active]
  if (opts.newTab && cur && cur.history.length) {
    saveDoc(p)
    const d: DocTab = { id: nextId++, history: [x], cursor: 0, tab: opts.tab ?? 'info', instance: opts.instance ?? -1 }
    p.docs.splice(p.active + 1, 0, d)
    if (opts.background) {
      api.Resource(x).then((r: any) => { g.labels[x] = label(r) }).catch(() => {})
      return
    }
    p.active = p.active + 1
    p.history = [x]
    p.cursor = 0
    p.tab = d.tab
    p.explicitTab = !!opts.tab
    load(p, x, false).then(() => { if (opts.instance !== undefined) p.instance = opts.instance })
    return
  }
  if (opts.tab) { p.tab = opts.tab; p.explicitTab = true }
  load(p, x, true).then(() => {
    if (opts.instance !== undefined) p.instance = opts.instance
  })
}

function closeDoc(p: Pane, i: number) {
  if (p.docs.length <= 1) {
    if (p === g.split) { closeSplit(); return }
    p.docs = []
    p.active = 0
    p.history = []
    p.cursor = -1
    p.current = ''
    p.resource = null
    return
  }
  const wasActive = i === p.active
  p.docs.splice(i, 1)
  if (wasActive) {
    p.active = -1
    activate(p, Math.min(i, p.docs.length - 1))
  } else if (i < p.active) p.active--
}

function closeSplit() {
  g.split = null
  g.focus = mainPane
}

export const paneById = (id: number): Pane | null => (id === 1 ? mainPane : g.split?.id === id ? g.split : null)

// moveDoc moves document tab i of pane `from` to position j of pane `to` (the same pane: reorder), with its history,
// view tab and selection. to = 'other': the other pane (the split is created if needed). The split closes when its
// last tab leaves it. docs/Explorer.md "Navigation".
export function moveDoc(from: Pane, i: number, to: Pane | 'other', j?: number) {
  if (i < 0 || i >= from.docs.length) return
  if (from.docs[from.active]) saveDoc(from)
  if (to === 'other') {
    if (from !== mainPane) to = mainPane
    else {
      if (!g.split) { g.split = newPane(); g.split.id = 2 }
      to = g.split
    }
  }
  const d = from.docs[i]!
  if (to === from) { // reorder: the active tab stays active
    const act = from.docs[from.active]
    from.docs.splice(i, 1)
    let k = j ?? from.docs.length
    if (k > i) k--
    from.docs.splice(Math.max(0, Math.min(k, from.docs.length)), 0, d)
    from.active = Math.max(0, from.docs.indexOf(act!))
    return
  }
  // leave the source pane
  if (from.docs.length === 1) {
    if (from === g.split) closeSplit()
    else { from.docs = []; from.active = 0; from.history = []; from.cursor = -1; from.current = ''; from.resource = null }
  } else {
    const wasActive = i === from.active
    from.docs.splice(i, 1)
    if (wasActive) { from.active = -1; activate(from, Math.min(i, from.docs.length - 1)) } else if (i < from.active) from.active--
  }
  // join the target pane and show it there
  if (to.docs[to.active] && to.docs[to.active]!.history.length) saveDoc(to)
  else if (to.docs[to.active]) to.docs.splice(to.active, 1) // an empty placeholder tab
  const k = Math.max(0, Math.min(j ?? to.docs.length, to.docs.length))
  to.docs.splice(k, 0, d)
  to.active = -1
  activate(to, k)
  g.focus = to
}

// moveDocToWindow opens a document tab in a new window (its current resource, view tab and selection) and closes it.
export function moveDocToWindow(from: Pane, i: number) {
  if (from.docs[from.active]) saveDoc(from)
  const d = from.docs[i]
  const x = d?.history[d.cursor]
  if (!d || !x) return
  const l = link(x, { tab: d.tab, instance: d.instance })
  api.OpenWindow(l).then(() => { flash('Opening a new window…'); closeDoc(from, i) }).catch((e: any) => flash(`New window failed: ${e}`))
}

// useExplorer binds the API to the pane of the calling component (PaneHost provides it; else the main pane).
export function useExplorer(pane?: Pane) {
  const p = pane ?? inject(PANE, mainPane)
  return {
    s: view(p),
    pane: p,
    open: (guid: string, opts: OpenOpts = {}) => open(p, guid, opts),
    // ow://<guid>[/<tab>[/<instance>]]
    openLink(l: string, opts: OpenOpts = {}) {
      const [x, tab, inst] = l.replace(/^ow:\/\//, '').split('/')
      open(p, x, { ...opts, tab: (tab as Tab) || undefined, instance: inst ? Number(inst) : undefined })
    },
    back() { if (p.cursor > 0) { p.cursor--; load(p, p.history[p.cursor], false) } },
    forward() { if (p.cursor < p.history.length - 1) { p.cursor++; load(p, p.history[p.cursor], false) } },
    // Jump to a history entry of the active tab (breadcrumbs).
    goto(k: number) { if (k >= 0 && k < p.history.length && k !== p.cursor) { p.cursor = k; load(p, p.history[k], false) } },
    activate: (i: number) => activate(p, i),
    closeDoc: (i: number) => closeDoc(p, i),
    closeSplit,
    async refresh() {
      g.status = await api.Status()
      if (g.status?.open) {
        g.types = await api.Types()
        g.packages = await api.Packages()
        if (g.status.index?.phase === 'done') g.kinds = await api.Kinds()
      }
      g.ui = await api.UIState()
    },
    async togglePin() {
      if (!p.resource) return
      g.ui = await api.SetPinned(p.resource.guid, !p.resource.pinned)
      p.resource.pinned = !p.resource.pinned
    },
    // Show a resource in the middle list: its type ("type") or its first package ("package").
    reveal(guid: string, where: 'type' | 'package', pkg?: string) {
      const x = parseGuid(guid)
      if (!x) return
      g.filter.search = ''
      if (where === 'package' && pkg) { g.filter.type = -1; g.filter.package = pkg; g.rail = 'packages' } else {
        g.filter.package = ''
        g.filter.type = guidType(x)
        g.rail = 'types'
      }
      g.reveal = { guid: x, n: (g.reveal?.n ?? 0) + 1 }
    },
    async copy(text: string, what = 'Copied') {
      try { await navigator.clipboard.writeText(text); flash(`${what}: ${text}`) } catch { flash('Copy failed') }
    },
    flash,
    label: (x: string) => g.labels[x] ?? x.replace(/^0+/, ''),
  }
}

let toastTimer: any
function flash(msg: string) {
  g.toast = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { g.toast = '' }, 1800)
}

// ---- MCP server (app_mcp.go, docs/Explorer.md "MCP server") ----

// mcpSnapshot is what the window shows, pushed to the backend (App.SetView) for the current_view tool.
export function mcpSnapshot() {
  const panes = [mainPane, ...(g.split ? [g.split] : [])].map((p) => ({
    id: p.id, current: p.current, error: p.error, tab: p.tab, instance: p.instance, path: p.path, graphNode: p.graphNode,
    resource: p.resource ? { guid: p.resource.guid, type: p.resource.type, typeLabel: p.resource.typeLabel, kind: p.resource.kind } : null,
    docs: p.docs.map((d) => { const x = d.history[d.cursor] ?? ''; return { guid: x, tab: d.tab, label: g.labels[x] ?? '' } }),
    active: p.active, history: p.history.length, extra: p.extra,
  }))
  return { focus: g.focus?.id ?? 1, wide: g.wide, mapMax: g.mapMax, mapCursor: g.mapCursor, rail: g.rail, filter: g.filter, panes }
}

// set: a value the watchers see change even when it is equal to the current one (-1 first, then the value).
function reselect(p: Pane, k: 'instance' | 'graphNode', v: number) {
  ;(p as any)[k] = -1
  nextTick(() => { (p as any)[k] = v })
}

// mcpDrive applies a drive command from Claude (the open / select tools; only sent while the user's toggle is on).
export function mcpDrive(c: any) {
  const pick = (w: string) => (w === 'split' ? g.split ?? mainPane : w === 'main' ? mainPane : g.focus ?? mainPane)
  if (c.op === 'open') {
    const p = c.where === 'split' ? mainPane : g.focus ?? mainPane
    open(p, c.guid, { tab: c.tab || undefined, instance: c.instance ?? undefined, newTab: c.where === 'tab' || c.where === 'background',
      background: c.where === 'background', split: c.where === 'split' })
    flash(`Claude opened ${g.labels[c.guid] ?? c.guid}`)
  } else if (c.op === 'select') {
    const p = pick(c.pane)
    if (c.tab) { p.tab = c.tab; saveDoc(p) }
    if (c.instance != null) reselect(p, 'instance', c.instance)
    if (c.path) p.path = c.path
    if (c.graphNode != null) reselect(p, 'graphNode', c.graphNode)
    nextTick(() => { g.driveTick++ })
    flash('Claude changed the selection')
  }
}
