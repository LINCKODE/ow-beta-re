<script setup lang="ts">
// "Ask Claude" panel (docs/Explorer.md "Ask Claude"): questions about a selection go to the Claude Code session that
// watches the explorer (MCP event stream); its replies (MCP tool `reply`) appear here.
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { ask, askCancel, askClear, askSend, askToggle, currentSubject, liveSubject, subjectLabel } from '~/composables/useAsk'
import { api } from '~/composables/api'
import { blocks, refKey, type Span } from '~/utils/mdlite'
import { NODE_KIND_COLORS } from '~/utils/format'
import { mainPane, useExplorer } from '~/composables/useExplorer'
import { guidType } from '~/utils/format'

const { copy, s: ex } = useExplorer()
// the Statescript graph a reply's #N refer to by default: the one the question was about
function graphOf(id: string) {
  const q = msgs.value.find((x: any) => x.role === 'user' && x.id === id)
  const g: string = q?.subject?.guid ?? ''
  return g && guidType(g.padStart(16, '0')) === 0x1b ? g : ''
}
// open a reference in the focused pane (Ctrl-click: a new tab)
function openRef(link: string, e: MouseEvent) {
  useExplorer(ex.focus ?? mainPane).openLink(link, e.ctrlKey || e.metaKey ? { newTab: true } : {})
}
const draft = ref('')
const busy = ref(false)
const err = ref('')
const input = ref<HTMLTextAreaElement>()
const log = ref<HTMLDivElement>()
const msgs = computed(() => ask.state.messages ?? [])
const subject = computed(() => currentSubject()) // follows the focused pane's selection unless pinned

// replies parsed once per change (badges need the graphs' node kinds and names: App.GraphNodes, cached)
const parsed = computed(() => msgs.value.map((m: any) => (m.role === 'claude' ? blocks(m.text, graphOf(m.id)) : null)))
const graphNodes = reactive<Record<string, Record<number, any> | null>>({})
watch(parsed, (ps) => {
  for (const bs of ps) for (const b of bs ?? []) for (const sp of b.spans) {
    if (sp.t === 'node' && !(sp.v in graphNodes)) {
      graphNodes[sp.v] = null
      api.GraphNodes(sp.v).then((ns: any[]) => { graphNodes[sp.v] = Object.fromEntries((ns ?? []).map((n) => [n.index, n])) }).catch(() => {})
    }
  }
}, { immediate: true })
const nodeOf = (sp: Span) => (sp.t === 'node' ? graphNodes[sp.v]?.[sp.n] : null)
// "#57 Wait": the link's own text if it has one, else the index and the node's short type
function nodeLabel(sp: Span) {
  if (sp.t !== 'node') return ''
  if (sp.label) return sp.label
  const n = nodeOf(sp)
  return n ? `#${sp.n} ${n.short}` : `#${sp.n}`
}

// two-way: the badges of what is selected now (not the pinned subject) are highlighted; a stepper walks them
const selKey = computed(() => {
  const s: any = liveSubject()
  if (!s) return ''
  if (s.kind === 'graphNode') return `g:${s.guid}:${s.node}`
  if (s.kind === 'placement') return `m:${s.map}:${s.record}`
  if (s.kind === 'field' && s.instance !== undefined && !s.path) return `t:${s.guid}:${s.instance}`
  if (s.kind === 'texture' && s.mip !== undefined) return `t:${s.guid}:${s.mip}`.replace(/^t/, 'x')
  return ''
})
const mentions = ref(0)
const mention = ref(0)
function badges() { return selKey.value && log.value ? Array.from(log.value.querySelectorAll(`[data-ref="${selKey.value}"]`)) as HTMLElement[] : [] }
// scrolls to a badge only when the selection changes or the stepper is clicked (showMention); new messages and
// re-renders just recount (countMentions), so the user's own scrolling is never taken over
function showMention(i: number) {
  const els = badges()
  mentions.value = els.length
  if (!els.length) return
  mention.value = ((i % els.length) + els.length) % els.length
  const el = els[mention.value]!, r = el.getBoundingClientRect(), lr = log.value!.getBoundingClientRect()
  if (r.top < lr.top + 28 || r.bottom > lr.bottom) el.scrollIntoView({ block: 'center', behavior: 'smooth' })
}
function countMentions() {
  const n = badges().length
  mentions.value = n
  if (mention.value >= n) mention.value = 0
}
watch(selKey, () => nextTick(() => showMention(0)))
watch(parsed, () => nextTick(countMentions))
const waiting = computed(() => msgs.value.filter((m: any) => m.role === 'user' && m.state === 'waiting'))
const QUICK = ['What is this?', 'What does it do in a match?', 'Where is it used?', 'What does the server have to do for it?']
const subscribe = computed(() => `watch the explorer (Monitor: while true; do curl -sN ${ask.state.events || 'http://127.0.0.1:17889/mcp/events'}; sleep 2; done)`)

watch(() => ask.focusTick, () => nextTick(() => input.value?.focus()))
// expanding mounts the panel after the focus request; restoring it at start-up (focusTick 0) doesn't take the keyboard
onMounted(() => { if (ask.focusTick) nextTick(() => input.value?.focus()) })
watch(() => msgs.value.length, () => nextTick(() => { if (log.value) log.value.scrollTop = log.value.scrollHeight }))

async function send(q?: string) {
  const text = (q ?? draft.value).trim()
  if (!text || busy.value) return
  busy.value = true
  err.value = ''
  try { await askSend(text); if (!q) draft.value = '' } catch (e: any) { err.value = String(e) } finally { busy.value = false }
}
// Ctrl+J collapses from inside the panel too (its keys don't reach the app's handler)
function panelKey(e: KeyboardEvent) { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'j') { e.preventDefault(); askToggle() } }
function key(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'j') { e.preventDefault(); e.stopPropagation(); askToggle(); return }
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  e.stopPropagation() // app shortcuts (Backspace = back, P = pin, …) stay out of the text box
}
const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
const now = ref(Date.now())
setInterval(() => { now.value = Date.now() }, 1000)
const since = (ms: number) => { const s = Math.max(0, Math.round((now.value - ms) / 1000)); return s < 60 ? `${s} s` : `${Math.floor(s / 60)} min ${s % 60} s` }
</script>

<template>
  <aside class="flex h-full min-h-0 flex-col border-l border-line-strong bg-panel" @keydown.stop="panelKey">
    <header class="flex items-center gap-2 border-b border-line px-3 py-2">
      <span class="text-focus">✦</span>
      <h2 class="font-medium">Ask Claude</h2>
      <span class="flex items-center gap-1.5 text-[12px]" :title="ask.state.listeners ? 'Claude Code sessions subscribed to the explorer\'s event stream' : 'No Claude Code session is listening'">
        <span class="h-1.5 w-1.5 rounded-full" :class="ask.state.listeners ? 'bg-v-ok' : 'bg-v-err'" />
        <span class="text-dim">{{ ask.state.listeners ? `${ask.state.listeners} session${ask.state.listeners > 1 ? 's' : ''} listening` : 'no session listening' }}</span>
      </span>
      <button class="ml-auto rounded px-2 py-0.5 text-[12px] text-dim hover:bg-raised hover:text-fg" title="Start a new conversation (unanswered questions are withdrawn)" @click="askClear">New</button>
      <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Collapse (Ctrl+J; the conversation stays)" @click="askToggle">⇥</button>
    </header>

    <div v-if="!ask.state.listeners" class="border-b border-line bg-well px-3 py-2 text-[12px] text-dim">
      No Claude Code session watches the explorer. In a Claude Code session that has the <span class="mono text-fg">owexplorer</span>
      MCP server (<span class="mono">.mcp.json</span>), say:
      <button class="mono mt-1 block w-full truncate rounded bg-panel px-2 py-1 text-left text-fg hover:bg-raised" title="Copy" @click="copy(subscribe, 'Copied')">{{ subscribe }}</button>
      Questions asked meanwhile wait and are delivered when it subscribes.
    </div>

    <div v-if="mentions" class="flex items-center gap-2 border-b border-line bg-sel/40 px-3 py-1 text-[12px]">
      <span class="text-focus">●</span>
      <span class="text-dim">the selection is mentioned {{ mentions }}×</span>
      <span class="mono ml-auto text-faint">{{ mention + 1 }}/{{ mentions }}</span>
      <button class="rounded px-1.5 text-dim hover:bg-raised hover:text-fg" title="Previous mention" @click="showMention(mention - 1)">↑</button>
      <button class="rounded px-1.5 text-dim hover:bg-raised hover:text-fg" title="Next mention" @click="showMention(mention + 1)">↓</button>
    </div>
    <div ref="log" class="selectable min-h-0 flex-1 overflow-auto px-3 py-2 text-[13px]">
      <p v-if="!msgs.length" class="mt-6 text-center text-faint">Ask about the selection below. The answer comes from your Claude Code session (its model and subscription), using the explorer's MCP tools and the repository's docs.</p>
      <div v-for="(m, k) in msgs" :key="k" class="mb-3">
        <div v-if="m.role === 'user'" class="ml-8 rounded-md bg-sel px-3 py-2">
          <div class="mb-1 flex items-center gap-2 text-[11px] text-dim">
            <span class="truncate">about {{ subjectLabel(m.subject) }}</span>
            <span class="ml-auto shrink-0">{{ time(m.at) }}</span>
          </div>
          <p class="whitespace-pre-wrap">{{ m.text }}</p>
          <div v-if="m.state === 'waiting'" class="mt-1 flex items-center gap-2 text-[11px] text-dim">
            <span class="animate-pulse text-focus">●</span> waiting for Claude · {{ since(m.at) }}
            <button class="ml-auto rounded px-1.5 text-dim hover:bg-raised hover:text-v-err" title="Withdraw the question" @click="askCancel(m.id)">stop</button>
          </div>
          <div v-else-if="m.state === 'cancelled'" class="mt-1 text-[11px] text-faint">withdrawn</div>
        </div>
        <div v-else class="mr-2">
          <div class="mb-1 flex items-center gap-2 text-[11px] text-dim"><span class="text-focus">✦ Claude</span><span class="ml-auto">{{ time(m.at) }}</span></div>
          <template v-for="(b, j) in parsed[k] ?? []" :key="j">
            <pre v-if="b.k === 'pre'" class="mono my-1 overflow-auto rounded bg-well p-2 text-[12px]">{{ b.text }}</pre>
            <component :is="b.k === 'h' ? 'h3' : 'p'" v-else class="my-1 leading-relaxed" :class="[b.k === 'h' ? 'mt-2 font-medium' : '', b.k === 'li' ? 'relative pl-4' : '']">
              <span v-if="b.k === 'li'" class="absolute left-0 text-faint">{{ b.n ? b.n + '.' : '•' }}</span>
              <template v-for="(sp, i) in b.spans" :key="i">
                <Guid v-if="sp.t === 'guid'" :guid="sp.v" /><button v-else-if="sp.t === 'link'" class="mono rounded-sm px-0.5 text-focus underline decoration-dotted underline-offset-2 hover:bg-raised" :title="`${sp.v}\nClick: open in the focused pane · Ctrl-click: new tab`" @click="(e: MouseEvent) => openRef(sp.v, e)">{{ sp.label }}</button><button v-else-if="sp.t === 'node'" class="badge" :class="refKey(sp) === selKey ? 'badge-hl' : ''" :data-ref="refKey(sp)" :title="`${nodeOf(sp)?.kind ?? 'node'} #${sp.n} of graph ${sp.v}${nodeOf(sp) ? ' · ' + nodeOf(sp)!.net : ''}\nClick: open the graph and centre the node · Ctrl-click: new tab`" @click="(e: MouseEvent) => openRef(`ow://${sp.v}/graph/${sp.n}`, e)"><span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ background: NODE_KIND_COLORS[nodeOf(sp)?.kind ?? 'Other'] }" />{{ nodeLabel(sp) }}</button><button v-else-if="sp.t === 'ref'" class="badge" :class="[refKey(sp) === selKey ? 'badge-hl' : '', 'badge-' + sp.kind]" :data-ref="refKey(sp)" :title="`${sp.kind === 'map' ? 'map record' : sp.kind === 'tree' ? 'instance' : 'mip'} ${sp.n} of ${sp.v}\nClick: open it · Ctrl-click: new tab`" @click="(e: MouseEvent) => openRef(`ow://${sp.v}/${sp.kind}/${sp.n}`, e)"><span class="text-[10px]">{{ sp.kind === 'map' ? '⌖' : sp.kind === 'tree' ? '⌥' : '▦' }}</span>{{ sp.label || (sp.kind === 'map' ? 'record ' : sp.kind === 'tree' ? 'instance ' : 'mip ') + sp.n }}</button><code v-else-if="sp.t === 'code'" class="mono rounded bg-well px-1 text-[12px]">{{ sp.v }}</code><strong v-else-if="sp.t === 'bold'">{{ sp.v }}</strong><em v-else-if="sp.t === 'em'">{{ sp.v }}</em><template v-else>{{ sp.v }}</template>
              </template>
            </component>
          </template>
        </div>
      </div>
    </div>

    <footer class="border-t border-line p-2">
      <div class="mb-1.5 flex items-center gap-2 text-[12px]">
        <span class="shrink-0 text-dim">Asking about</span>
        <span class="flex min-w-0 flex-1 items-center gap-1 rounded-full border px-2 py-0.5" :class="ask.pinned ? 'border-focus/60 bg-sel' : 'border-line'" :title="(ask.pinned ? 'Pinned: stays while you select other things' : 'Follows your selection') + '\n' + JSON.stringify(subject)">
          <span class="min-w-0 flex-1 truncate text-fg">{{ subjectLabel(subject) }}</span>
          <button class="shrink-0 hover:text-fg" :class="ask.pinned ? 'text-focus' : 'text-faint'" :title="ask.pinned ? 'Unpin: follow the selection again' : 'Pin: keep this subject while selecting other things'" @click="ask.pinned = ask.pinned ? null : subject">{{ ask.pinned ? '📌' : '⚲' }}</button>
        </span>
      </div>
      <div class="mb-1.5 flex flex-wrap gap-1">
        <button v-for="q in QUICK" :key="q" class="rounded border border-line px-2 py-0.5 text-[11.5px] text-dim hover:bg-raised hover:text-fg" :disabled="busy" @click="send(q)">{{ q }}</button>
      </div>
      <textarea ref="input" v-model="draft" rows="3" placeholder="Ask about the selection… (Enter sends, Shift+Enter new line)" class="w-full resize-none rounded border border-line bg-well px-2 py-1.5 placeholder:text-faint" @keydown="key" />
      <div class="mt-1 flex items-center gap-2 text-[11px] text-faint">
        <span v-if="err" class="text-v-err">{{ err }}</span>
        <span v-else-if="waiting.length">{{ waiting.length }} waiting</span>
        <span v-else>Follow-ups continue the same conversation.</span>
        <button class="ml-auto rounded bg-sel px-3 py-1 text-[12px] text-fg hover:bg-raised disabled:opacity-50" :disabled="busy || !draft.trim()" @click="send()">Send</button>
      </div>
    </footer>
  </aside>
</template>

<style scoped>
.badge { display: inline-flex; align-items: center; gap: 4px; margin: 0 1px; padding: 0 5px; border-radius: 3px; border: 1px solid transparent; background: var(--color-raised); font-family: var(--font-mono); font-size: 11.5px; line-height: 18px; color: var(--color-fg); vertical-align: 1px; white-space: nowrap; }
.badge:hover { border-color: var(--color-line-strong); background: color-mix(in srgb, var(--color-raised) 70%, var(--color-fg) 10%); }
.badge-map { color: var(--color-v-ok); }
.badge-tree { color: var(--color-v-obj); }
.badge-texture { color: var(--color-v-string); }
.badge-hl { border-color: var(--color-focus); background: var(--color-sel); box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-focus) 35%, transparent); }
</style>
