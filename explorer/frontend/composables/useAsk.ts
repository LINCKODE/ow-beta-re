// "Ask Claude" (app_ask.go, docs/Explorer.md "Ask Claude"): questions about a selection go to the Claude Code session
// that watches the explorer's MCP event stream; its replies (MCP tool `reply`) come back here.
import { reactive } from 'vue'
import { api, onEvent } from './api'
import { mainPane, useExplorer } from './useExplorer'

export interface AskSubject {
  kind: 'placement' | 'graphNode' | 'resource' | 'texture' | 'field' | 'view' | 'model' | 'submesh' | 'material'
  label?: string
  guid?: string
  map?: string
  record?: number
  node?: number
  instance?: number // tree instance; for a placement, the model instance picked in 3D (T064)
  submesh?: number
  look?: string
  path?: string
  mip?: number
}

export const ask = reactive({
  open: false,
  pinned: null as AskSubject | null, // a subject kept while the selection moves elsewhere (the chip's pin)
  state: { listeners: 0, hub: 0, events: '', messages: [] as any[], error: '' } as any,
  focusTick: 0, // the panel focuses its input when this changes
  unread: 0,    // replies that arrived while the panel was collapsed (the thumb's badge)
  seen: 0,      // replies counted as read
})

const replies = () => (ask.state.messages ?? []).filter((m: any) => m.role === 'claude').length

let started = false
export async function askRefresh() {
  try { ask.state = await api.AskState() } catch { return }
  const n = replies()
  if (ask.open || n < ask.seen) ask.seen = n // read (or a new conversation)
  ask.unread = n - ask.seen
}
export function askStart() {
  if (started) return
  started = true
  onEvent('ask', askRefresh)
  setInterval(() => { if (ask.open) askRefresh() }, 3000) // listener count of the hub (another window)
}

// liveSubject is what the focused pane shows selected: the subject its view published (pane.extra.ask, for its tab:
// placement, graph node, tree field, texture mip, dependency node), else the open resource.
export function liveSubject(): AskSubject | null {
  const { s } = useExplorer(mainPane)
  const p: any = s.focus ?? mainPane
  const a = p.extra?.ask
  if (a && a.tab === p.tab && a.subject) return a.subject
  if (p.resource) return { kind: 'resource', guid: p.resource.guid, label: `${p.resource.name?.text || p.resource.typeLabel + ' ' + p.resource.guid}` }
  return null
}
// the subject a question goes to: the pinned one, else the live selection
export function currentSubject(): AskSubject | null { return ask.pinned ?? liveSubject() }
const same = (a: AskSubject | null, b: AskSubject | null) => JSON.stringify({ ...a, label: '' }) === JSON.stringify({ ...b, label: '' })

// askAbout (the Ask buttons) opens the panel and focuses the input; a subject other than the live selection (e.g. a
// GUID's menu) is pinned.
export function askAbout(subject: AskSubject) {
  askStart()
  ask.pinned = same(subject, liveSubject()) ? null : subject
  ask.open = true
  ask.focusTick++
  askRefresh()
}

// askToggle expands / collapses the panel (the thumb on the window edge, Ctrl+J, the status bar's ✦ Ask).
export function askToggle() {
  askStart()
  ask.open = !ask.open
  if (ask.open) { ask.focusTick++; askRefresh() }
}

// the last question the user asked (a new question continues that conversation)
function lastAsk() {
  const m = [...ask.state.messages].reverse().find((x: any) => x.role === 'user')
  return m?.id ?? ''
}

export async function askSend(question: string) {
  const subject = currentSubject() ?? { kind: 'view', label: 'what I am looking at' }
  await api.Ask(question, subject, lastAsk())
  await askRefresh()
}
export async function askCancel(id: string) { await api.AskCancel(id); await askRefresh() }
export async function askClear() { await api.AskClear(); await askRefresh() }

// subjectLabel is a subject in a few words.
export function subjectLabel(s: AskSubject | null | undefined): string {
  if (!s) return 'what I am looking at'
  if (s.label) return s.label
  switch (s.kind) {
    case 'placement': return `map record ${s.record}`
    case 'graphNode': return `graph node #${s.node}`
    case 'texture': return `texture ${s.guid}${s.mip !== undefined ? ` mip ${s.mip}` : ''}`
    case 'field': return `field ${s.path || s.instance}`
    case 'resource': return s.guid ?? 'resource'
  }
  return 'what I am looking at'
}
