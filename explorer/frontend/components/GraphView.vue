<script setup lang="ts">
// Statescript graph: node list by kind or the canvas, with the selected node's details on the right. The canvas
// uses the layered layout (Go, view/layout.go: left → right, few crossings) or the editor's m_pos; nodes can be
// dragged (view only, saved per graph in the config directory; "Reset layout"). ow://<graph>/graph/<node index>
// selects and centres a node.
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { usePanel } from '~/composables/usePanel'
import { NET_COLORS, NET_LABELS, NODE_KIND_COLORS } from '~/utils/format'

const p = defineProps<{ guid: string }>()
const { s, pane } = useExplorer()
const g = ref<any>(null)
const err = ref('')
const mode = ref<'list' | 'canvas'>('canvas')
const filter = ref('')
const KINDS = ['Entry', 'Condition', 'Action', 'Terminator', 'State', 'Other']
const kindColor = NODE_KIND_COLORS
// network side (view/graph.go netSide): the filter greys out the nodes (and links) of the sides that are off
const NETS = ['client', 'both', 'server'] as const
const netOn = reactive<Record<string, boolean>>({ client: true, both: true, server: true })
const allNets = computed(() => NETS.every((k) => netOn[k]))
function toggleNet(k: string) {
  if (allNets.value) { for (const x of NETS) netOn[x] = x === k; return } // from "all": show only this one
  netOn[k] = !netOn[k]
  if (!NETS.some((x) => netOn[x])) for (const x of NETS) netOn[x] = true
}
const lit = (i: number) => netOn[g.value.nodes[i]?.net] !== false

onMounted(async () => {
  try {
    g.value = await api.Graph(p.guid)
    await loadPositions()
    fit()
    await nextTick()
    focusIndex(s.instance)
  } catch (e: any) { err.value = String(e) }
})

// ---- layout: layered (default) or the editor's m_pos, plus the positions the user dragged ----
const editorLayout = usePanel('graph.editorLayout', 0) // 1 = m_pos (SetPanel keeps positive values only)
const layoutMode = computed(() => (editorLayout.value ? 'editor' : 'layered'))
const over = reactive<Record<string, [number, number]>>({}) // node position → dragged x, y
async function loadPositions() {
  for (const k of Object.keys(over)) delete over[k]
  try { Object.assign(over, await api.GraphPositions(p.guid, layoutMode.value)) } catch {}
}
function pos(i: number): [number, number] {
  const o = over[i]
  if (o) return o
  const n = g.value.nodes[i]
  if (!editorLayout.value && g.value.layout?.pos?.[i]) return g.value.layout.pos[i]
  return [n.x, n.y]
}
async function setLayout(editor: boolean) {
  if (!!editorLayout.value === editor) return
  editorLayout.value = editor ? 1 : 0
  editorLayout.commit(!editor)
  await loadPositions()
  fit()
}
const moved = computed(() => Object.keys(over).length)
async function resetLayout() {
  for (const k of Object.keys(over)) delete over[k]
  await api.SetGraphPositions(p.guid, layoutMode.value, {})
}
// ow://<graph>/graph/<node index> (and Ask replies' node links): the pane's instance is an m_nodes index here
function focusIndex(idx: number) {
  if (!g.value || idx < 0) return
  const i = g.value.nodes.findIndex((n: any) => n.index === idx)
  if (i >= 0) { mode.value = 'canvas'; focusNode(i) }
}
watch(() => s.instance, (i) => focusIndex(i))

const sel = computed(() => (g.value && s.graphNode >= 0 ? g.value.nodes[s.graphNode] : null))
const outLinks = computed(() => g.value?.links.filter((l: any) => l.from === s.graphNode) ?? [])
const inLinks = computed(() => g.value?.links.filter((l: any) => l.to === s.graphNode) ?? [])
function label(n: any) { return n.display || n.short }

const groups = computed(() => {
  if (!g.value) return []
  const f = filter.value.trim().toLowerCase()
  return KINDS.map((k) => ({
    kind: k,
    nodes: g.value.nodes.map((n: any, i: number) => ({ n, i })).filter(({ n }: any) => n.kind === k &&
      (!f || n.type.toLowerCase().includes(f) || (n.display ?? '').toLowerCase().includes(f) || String(n.index) === f ||
        n.params.some((x: any) => x.summary.toLowerCase().includes(f)))),
  })).filter((x) => x.nodes.length)
})

// ---- canvas ----
const W = 190, H = 46
const view = reactive({ x: 0, y: 0, k: 1 })
const box = ref<SVGSVGElement>()
function bounds() {
  const ns = g.value?.nodes ?? []
  if (!ns.length) return { x0: 0, y0: 0, x1: 1, y1: 1 }
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  ns.forEach((_: any, i: number) => { const [x, y] = pos(i); x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x + W); y1 = Math.max(y1, y + H) })
  return { x0, y0, x1, y1 }
}
function fit() {
  const b = bounds()
  const r = box.value?.getBoundingClientRect() ?? { width: 900, height: 600 }
  // Editor positions: fit the width (tall graphs are panned vertically), top-aligned. Layered: the whole graph, centred.
  const kw = (r.width - 40) / (b.x1 - b.x0 + 80), kh = (r.height - 40) / (b.y1 - b.y0 + 80)
  const k = Math.min(editorLayout.value ? kw : Math.min(kw, kh), 1.2)
  view.k = k
  view.x = -b.x0 * k + (r.width - (b.x1 - b.x0) * k) / 2
  view.y = editorLayout.value ? -b.y0 * k + 40 : -b.y0 * k + (r.height - (b.y1 - b.y0) * k) / 2
}
function wheel(e: WheelEvent) {
  const r = box.value!.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  const f = Math.exp(-e.deltaY * 0.0015)
  const k = Math.min(3, Math.max(0.05, view.k * f))
  view.x = mx - (mx - view.x) * (k / view.k)
  view.y = my - (my - view.y) * (k / view.k)
  view.k = k
}
let drag: { x: number; y: number } | null = null
// dragging the background pans; a click on it without a drag clears the selection (also Esc, or ✕ in the side panel)
let bg: null | { x: number; y: number; moved: boolean } = null
function down(e: MouseEvent) { drag = { x: e.clientX - view.x, y: e.clientY - view.y }; bg = e.button === 0 ? { x: e.clientX, y: e.clientY, moved: false } : null }
function move(e: MouseEvent) {
  if (drag) { view.x = e.clientX - drag.x; view.y = e.clientY - drag.y }
  if (bg && Math.abs(e.clientX - bg.x) + Math.abs(e.clientY - bg.y) > 3) bg.moved = true
}
function up(e?: MouseEvent) {
  drag = null
  if (bg && !bg.moved && e?.type === 'mouseup') clearSel()
  bg = null
}
function clearSel() { s.graphNode = -1 }
function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape' || s.graphNode < 0 || !box.value?.isConnected) return
  const t = e.target as HTMLElement | null
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
  if ((s.focus ?? pane) !== pane) return
  clearSel()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
// a link: through the layered layout's route (bends at its dummy nodes) unless an end was dragged; curves leave and
// enter the ports horizontally
function path(l: any, k: number) {
  const [ax, ay] = pos(l.from), [bx, by] = pos(l.to)
  let pts: number[][] = [[ax + W, ay + H / 2], [bx, by + H / 2]]
  const r = g.value.layout?.routes?.[k]
  if (!editorLayout.value && r && !over[l.from] && !over[l.to]) pts = r
  let d = `M${pts[0]![0]},${pts[0]![1]}`
  for (let i = 1; i < pts.length; i++) {
    const [x1, y1] = pts[i - 1]!, [x2, y2] = pts[i]!
    const dx = Math.max(30, Math.abs(x2! - x1!) / 2)
    d += ` C${x1! + dx},${y1} ${x2! - dx},${y2} ${x2},${y2}`
  }
  return d
}
const containers = computed(() => (g.value?.containers ?? []).filter((c: any) => c.nodes.length).map((c: any) => {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const i of c.nodes) { const [x, y] = pos(i); x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x + W); y1 = Math.max(y1, y + H) }
  return { ...c, x0: x0 - 16, y0: y0 - 30, w: x1 - x0 + 32, h: y1 - y0 + 46 }
}))
function focusNode(i: number) {
  s.graphNode = i
  if (mode.value !== 'canvas') return
  const [x, y] = pos(i), r = box.value!.getBoundingClientRect()
  view.x = r.width / 2 - (x + W / 2) * view.k
  view.y = r.height / 2 - (y + H / 2) * view.k
}
// dragging a node moves it (view only); a click without a drag selects it
let nd: null | { i: number; sx: number; sy: number; ox: number; oy: number; moved: boolean } = null
function nodeDown(e: MouseEvent, i: number) {
  if (e.button !== 0) return
  e.stopPropagation()
  e.preventDefault()
  const [x, y] = pos(i)
  nd = { i, sx: e.clientX, sy: e.clientY, ox: x, oy: y, moved: false }
  window.addEventListener('mousemove', nodeMove, true)
  window.addEventListener('mouseup', nodeUp, true)
}
function nodeMove(e: MouseEvent) {
  if (!nd) return
  const dx = e.clientX - nd.sx, dy = e.clientY - nd.sy
  if (!nd.moved && Math.abs(dx) + Math.abs(dy) < 4) return
  nd.moved = true
  over[nd.i] = [Math.round(nd.ox + dx / view.k), Math.round(nd.oy + dy / view.k)]
}
function nodeUp() {
  window.removeEventListener('mousemove', nodeMove, true)
  window.removeEventListener('mouseup', nodeUp, true)
  if (!nd) return
  if (nd.moved) api.SetGraphPositions(p.guid, layoutMode.value, { ...over }).catch(() => {})
  else s.graphNode = nd.i
  nd = null
}
// the Ask panel follows the selected node (useAsk liveSubject)
watch(sel, (n) => {
  if (n) s.extra.ask = { tab: 'graph', subject: { kind: 'graphNode', guid: p.guid, node: n.index, label: `${n.kind} #${n.index} ${n.short}${n.display ? ' “' + n.display + '”' : ''} in graph ${p.guid}` } }
  else if (s.extra.ask?.tab === 'graph') delete s.extra.ask
}, { immediate: true })
onBeforeUnmount(() => { if (s.extra.ask?.tab === 'graph') delete s.extra.ask })
// an MCP select moves the view to the node it selected
watch(() => s.driveTick, () => { if (g.value && s.graphNode >= 0 && s.graphNode < g.value.nodes.length) nextTick(() => focusNode(s.graphNode)) })
function openInstance(i: number) { s.tab = 'tree'; s.instance = i }
const sideW = usePanel('graph.side', 300)
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else class="flex h-full min-h-0">
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <div class="flex items-center gap-3 border-b border-line px-3 py-1.5 text-[12px]">
        <div class="flex rounded border border-line">
          <button class="px-2.5 py-0.5" :class="mode === 'canvas' ? 'bg-raised text-fg' : 'text-dim'" @click="mode = 'canvas'">Canvas</button>
          <button class="px-2.5 py-0.5" :class="mode === 'list' ? 'bg-raised text-fg' : 'text-dim'" @click="mode = 'list'">Nodes</button>
        </div>
        <template v-if="g">
          <span class="min-w-0 truncate whitespace-nowrap text-dim" :title="`${g.syncVars} sync vars, ${g.remoteSync} remote-synced nodes`">{{ g.nodes.length }} nodes, {{ g.links.length }} links, {{ g.states }} states, {{ g.syncVars }} sync vars</span>
        </template>
        <template v-if="mode === 'canvas'">
          <div class="ml-auto flex items-center rounded border border-line" title="Network side: nodes of the sides that are off are greyed out">
            <button class="px-2 py-0.5" :class="allNets ? 'bg-raised text-fg' : 'text-dim'" @click="NETS.forEach((x) => (netOn[x] = true))">All</button>
            <button v-for="k in NETS" :key="k" class="flex items-center gap-1 px-2 py-0.5" :class="!allNets && netOn[k] ? 'bg-raised text-fg' : 'text-dim'" :title="NET_LABELS[k]" @click="toggleNet(k)">
              <span class="h-2 w-2 rounded-full" :style="{ background: NET_COLORS[k] }" />{{ k === 'both' ? 'Both' : k === 'client' ? 'Client' : 'Server' }}
            </button>
          </div>
          <div class="flex rounded border border-line" title="Canvas layout (view only: the game data is not changed)">
            <button class="px-2 py-0.5" :class="!editorLayout ? 'bg-raised text-fg' : 'text-dim'" title="Layered: links left → right, entries first, few crossings" @click="setLayout(false)">Layered</button>
            <button class="px-2 py-0.5" :class="editorLayout ? 'bg-raised text-fg' : 'text-dim'" title="The editor's positions (m_pos)" @click="setLayout(true)">Editor</button>
          </div>
          <button v-if="moved" class="text-dim hover:text-fg" :title="`${moved} dragged node${moved > 1 ? 's' : ''}: back to the computed positions`" @click="resetLayout">Reset layout</button>
          <button class="text-dim hover:text-fg" @click="fit">Fit</button>
        </template>
      </div>

      <div v-show="mode === 'canvas'" class="relative min-h-0 flex-1 overflow-hidden bg-well">
        <svg ref="box" class="h-full w-full" @wheel.prevent="wheel" @mousedown="down" @mousemove="move" @mouseup="up" @mouseleave="up">
          <defs>
            <pattern id="grid" width="24" height="24" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="0.8" fill="#253049" /></pattern>
          </defs>
          <rect width="100%" height="100%" fill="url(#grid)" />
          <g v-if="g" :transform="`translate(${view.x},${view.y}) scale(${view.k})`">
            <g v-for="c in (editorLayout ? containers : [])" :key="'c' + c.instance">
              <rect :x="c.x0" :y="c.y0" :width="c.w" :height="c.h" rx="8" fill="#1c263833" stroke="#34425f" stroke-dasharray="4 4" />
              <text :x="c.x0 + 10" :y="c.y0 + 18" fill="#8a98b0" font-size="12">{{ c.title || c.type }}</text>
            </g>
            <path v-for="(l, i) in g.links" :key="'l' + i" :d="path(l, i)" fill="none" :opacity="lit(l.from) && lit(l.to) ? 1 : 0.12"
              :stroke="l.from === s.graphNode || l.to === s.graphNode ? '#8fb4ff' : '#4a5a7a'" :stroke-width="l.from === s.graphNode || l.to === s.graphNode ? 2 : 1.2" />
            <g v-for="(n, i) in g.nodes" :key="'n' + i" :transform="`translate(${pos(i)[0]},${pos(i)[1]})`" class="cursor-grab" :opacity="lit(i) ? 1 : 0.18" @mousedown="(e: MouseEvent) => nodeDown(e, i)" @dblclick="openInstance(n.instance)">
              <rect :width="W" :height="H" rx="6" :fill="i === s.graphNode ? '#24406b' : '#151d2b'" :stroke="i === s.graphNode ? '#8fb4ff' : '#34425f'" />
              <rect :width="4" :height="H" rx="2" :fill="kindColor[n.kind]" />
              <text x="12" y="19" fill="#d8e0ec" font-size="12.5" font-family="Instrument Sans Variable">{{ label(n).slice(0, 26) }}</text>
              <text x="12" y="36" fill="#5b6780" font-size="11" font-family="JetBrains Mono Variable">#{{ n.index }}<tspan v-if="n.stateIndex >= 0"> s{{ n.stateIndex }}</tspan><tspan v-if="n.remoteSync >= 0" fill="#7fd6c2"> sync{{ n.remoteSync }}</tspan></text>
              <circle :cx="W - 10" cy="10" r="4" :fill="NET_COLORS[n.net]" :stroke="n.netConflict ? '#ff5a4a' : 'none'" stroke-width="2"><title>{{ NET_LABELS[n.net] }}: {{ n.netWhy }}{{ n.netConflict ? ' · conflict: ' + n.netConflict : '' }}</title></circle>
            </g>
          </g>
        </svg>
        <div class="pointer-events-none absolute top-2 right-3 flex gap-3 rounded bg-panel/90 px-2 py-1 text-[11px] text-dim">
          <span v-for="k in KINDS" :key="k" class="flex items-center gap-1"><span class="h-2 w-2 rounded-sm" :style="{ background: kindColor[k] }" />{{ k }}</span>
          <span class="text-faint">·</span>
          <span v-for="k in NETS" :key="k" class="flex items-center gap-1" :title="NET_LABELS[k]"><span class="h-2 w-2 rounded-full" :style="{ background: NET_COLORS[k] }" />{{ k }}</span>
        </div>
        <p class="pointer-events-none absolute bottom-2 left-3 rounded bg-panel/90 px-2 py-0.5 text-[11px] text-faint">Drag the background to pan, a node to move it (view only), scroll to zoom, double-click a node to open its instance. {{ editorLayout ? "Positions: the editor's m_pos." : 'Layered layout.' }}</p>
      </div>

      <div v-show="mode === 'list'" class="flex min-h-0 flex-1 flex-col">
        <input v-model="filter" placeholder="Filter nodes by type, text, index or parameter" class="m-2 rounded border border-line bg-well px-2 py-1 placeholder:text-faint" />
        <div class="min-h-0 flex-1 overflow-auto pb-4">
          <section v-for="grp in groups" :key="grp.kind">
            <h3 class="sticky top-0 z-10 flex items-center gap-2 border-y border-line bg-panel px-3 py-1 text-[12px] font-medium">
              <span class="h-2 w-2 rounded-full" :style="{ background: kindColor[grp.kind] }" />{{ grp.kind }}<span class="text-faint">{{ grp.nodes.length }}</span>
            </h3>
            <button v-for="{ n, i } in grp.nodes" :key="i" class="flex w-full items-baseline gap-3 px-3 py-1 text-left hover:bg-raised" :class="i === s.graphNode ? 'bg-sel' : ''" @click="s.graphNode = i">
              <span class="h-2 w-2 shrink-0 self-center rounded-full" :style="{ background: NET_COLORS[n.net], opacity: netOn[n.net] ? 1 : 0.3 }" :title="NET_LABELS[n.net]" />
              <span class="mono w-10 shrink-0 text-right text-faint">#{{ n.index }}</span>
              <span class="w-56 shrink-0 truncate" :class="n.named ? '' : 'mono text-dim'" :title="n.type">{{ n.short }}</span>
              <span class="min-w-0 flex-1 truncate text-dim">{{ n.display || n.params.map((x: any) => x.summary).join('  ') }}</span>
            </button>
          </section>
        </div>
      </div>
    </div>

    <Splitter id="graph.side" :def="300" :min="200" :max="900" side="after" />
    <aside class="selectable min-h-0 shrink-0 overflow-auto border-l border-line p-4 text-[12.5px]" :style="{ width: sideW + 'px' }">
      <p v-if="!sel" class="text-dim">Select a node to see its plugs, links and parameters.</p>
      <template v-else>
        <div class="flex items-center gap-2"><span class="h-2.5 w-2.5 rounded-full" :style="{ background: kindColor[sel.kind] }" /><span class="text-dim">{{ sel.kind }} #{{ sel.index }}</span><button class="ml-auto rounded px-1.5 text-dim hover:bg-raised hover:text-fg" title="Clear the selection (Esc, or click the empty canvas)" @click="clearSel">✕</button></div>
        <h3 class="mt-1 text-[15px] leading-snug break-words" :class="sel.named ? '' : 'mono'">{{ sel.type }}</h3>
        <div class="mt-1 flex flex-wrap items-center gap-1"><OpenIn :guid="p.guid" tab="tree" :instance="sel.instance" label="instance" /><OpenIn :ask="{ kind: 'graphNode', guid: p.guid, node: sel.index, label: `${sel.kind} #${sel.index} ${sel.short}${sel.display ? ' “' + sel.display + '”' : ''} in graph ${p.guid}` }" /></div>
        <p v-if="sel.display" class="mt-1">{{ sel.display }}</p>
        <p v-if="sel.comment" class="mt-1 text-dim">{{ sel.comment }}</p>
        <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[12px]">
          <dt class="text-dim">Instance</dt><dd><button class="mono text-v-obj hover:underline" @click="openInstance(sel.instance)">→ {{ sel.instance }}</button></dd>
          <dt class="text-dim">Unique id</dt><dd class="mono">{{ sel.uniqueId }}</dd>
          <dt class="text-dim">State index</dt><dd class="mono">{{ sel.stateIndex >= 0 ? sel.stateIndex : '–' }}</dd>
          <dt class="text-dim">Remote sync</dt><dd class="mono">{{ sel.remoteSync >= 0 ? sel.remoteSync : '–' }}</dd>
          <dt class="text-dim">Scope</dt><dd class="mono">{{ sel.scope }}</dd>
          <dt class="text-dim">Network</dt>
          <dd><span class="mr-1 inline-block h-2 w-2 rounded-full" :style="{ background: NET_COLORS[sel.net] }" />{{ NET_LABELS[sel.net] }} <span class="text-faint">({{ sel.netWhy }})</span><div v-if="sel.netConflict" class="text-v-err">conflict: {{ sel.netConflict }}</div></dd>
        </dl>
        <h4 class="mt-4 mb-1 font-medium">Links out</h4>
        <p v-if="!outLinks.length" class="text-faint">none</p>
        <button v-for="(l, k) in outLinks" :key="'o' + k" class="flex w-full gap-2 rounded px-1 py-0.5 text-left hover:bg-raised" @click="focusNode(l.to)">
          <span class="text-dim">{{ l.fromPlug }}</span><span class="text-faint">→</span><span class="truncate">#{{ g.nodes[l.to].index }} {{ label(g.nodes[l.to]) }}</span>
        </button>
        <h4 class="mt-3 mb-1 font-medium">Links in</h4>
        <p v-if="!inLinks.length" class="text-faint">none</p>
        <button v-for="(l, k) in inLinks" :key="'i' + k" class="flex w-full gap-2 rounded px-1 py-0.5 text-left hover:bg-raised" @click="focusNode(l.from)">
          <span class="truncate">#{{ g.nodes[l.from].index }} {{ label(g.nodes[l.from]) }}</span><span class="text-faint">→</span><span class="text-dim">{{ l.toPlug }}</span>
        </button>
        <h4 class="mt-3 mb-1 font-medium">Parameters</h4>
        <p v-if="!sel.params.length" class="text-faint">none</p>
        <div v-for="x in sel.params" :key="x.name + x.instance" class="mb-1.5">
          <button class="text-dim hover:text-fg" @click="openInstance(x.instance)">{{ x.name }}</button>
          <div class="mono break-words text-[11.5px] text-v-num">{{ x.summary }}</div>
          <div v-for="(rf, k) in x.refs" :key="k" class="flex items-baseline gap-2 text-[11.5px]">
            <Guid :guid="rf.guid" /><span class="truncate text-dim" :title="rf.path">{{ rf.name || rf.path }}</span>
          </div>
        </div>
      </template>
    </aside>
  </div>
</template>
