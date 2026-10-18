<script setup lang="ts">
// Dependency graph of any resource, from the reference index. The resource sits in the middle; what it uses
// grows to the right, what uses it to the left. Every node expands on demand (◂ used by, uses ▸); a side
// with more than CAP neighbours shows one group node per type instead, which expands (≤ CAP) or opens the
// list (list mode for huge fan-ins). Edges always point left → right ("uses"). Filters (kinds, types,
// package) apply to every expansion: changing them replays the expansions.
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { hex3, kindColor, num, typeColor } from '~/utils/format'
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ guid: string }>()
const { s, open, pane } = useExplorer()
const CAP = 30
const W = 236, H = 50, ROWH = 60, COLW = W + 130

interface GNode {
  key: string; col: number; seq: number; parent: string
  row?: any                                                           // resource (NodeRow)
  group?: { owner: string; dir: 'in' | 'out'; type: number; label: string; count: number }
}
interface GEdge { from: string; to: string; kinds: string[]; aux: number }

const nodes = reactive(new Map<string, GNode>())
const edges = reactive(new Map<string, GEdge>())
const expanded = reactive(new Set<string>()) // "<guid>:<dir>"
let order: string[] = []                     // expansions in the order they were made (replayed on filter change)
let seq = 0
const sel = ref(p.guid)
// MCP current_view: the selected node
watch(sel, (x) => {
  s.extra.deps = { selected: x, nodes: nodes.size }
  // the Ask panel follows the selected node when it isn't the resource itself (useAsk liveSubject)
  if (x && x !== p.guid) s.extra.ask = { tab: 'deps', subject: { kind: 'resource', guid: x, label: nodes.get(x)?.row?.name || x } }
  else if (s.extra.ask?.tab === 'deps') delete s.extra.ask
}, { immediate: true })
onBeforeUnmount(() => { delete s.extra.deps; if (s.extra.ask?.tab === 'deps') delete s.extra.ask })
const list = ref<null | { owner: string; dir: 'in' | 'out'; types: number[] }>(null)
const why = ref<any>(null)
const busy = ref(false)
const err = ref('')

// ---- filters ----
const isPkg = computed(() => s.resource?.type === 0x077)
const kindsOn = ref<string[]>([])
const typesOn = ref<number[]>([])
const pkg = ref('')
const noMissing = ref(true) // hide ids that are not resources (config-var ids, …)
function defaultKinds() {
  // Package membership swamps "used by" (every resource is in a package): off unless the root is a package.
  kindsOn.value = s.kinds.map((k: any) => k.name).filter((k: string) => isPkg.value || k !== 'package.member')
}

// ---- building ----
function addNode(n: GNode) {
  if (!nodes.has(n.key)) nodes.set(n.key, n)
  return nodes.get(n.key)!
}
function addEdge(from: string, to: string, kinds: string[], aux = 0) {
  const k = `${from}>${to}`
  const e = edges.get(k)
  if (e) { e.kinds = [...new Set([...e.kinds, ...kinds])]; return }
  edges.set(k, { from, to, kinds, aux })
}
function link(owner: string, dir: 'in' | 'out', other: string, kinds: string[], aux: number) {
  if (dir === 'out') addEdge(owner, other, kinds, aux)
  else addEdge(other, owner, kinds, aux)
}

async function expand(guid: string, dir: 'in' | 'out', types: number[] = []) {
  const key = `${guid}:${dir}${types.length ? ':' + types.join(',') : ''}`
  if (expanded.has(key)) return
  const owner = nodes.get(guid)
  if (!owner) return
  const col = owner.col + (dir === 'out' ? 1 : -1)
  const q = { guid, dir, kinds: kindsOn.value, types: types.length ? types : typesOn.value, package: pkg.value.trim(), search: '', noMissing: noMissing.value, start: 0, count: CAP }
  busy.value = true
  try {
    const pg = await api.Neighbors(q)
    if (!pg.ready) { err.value = 'The reference index is still being built.'; return }
    expanded.add(key)
    if (!order.includes(key)) order.push(key)
    if (pg.total <= CAP) {
      // nodes replacing a type group take its place in the column
      const base = types.length ? nodes.get(`grp:${guid}:${dir}:${types[0]}`)?.seq : undefined
      for (const [i, r] of pg.rows.entries()) {
        const n = addNode({ key: r.guid, col, seq: base !== undefined ? base + (i + 1) / 1000 : seq++, parent: guid, row: r })
        link(guid, dir, n.key, r.kinds, r.aux)
      }
      // a group node this expansion replaces
      for (const t of types) {
        const gk = `grp:${guid}:${dir}:${t}`
        nodes.delete(gk)
        edges.delete(`${guid}>${gk}`)
        edges.delete(`${gk}>${guid}`)
      }
    } else {
      for (const t of pg.byType) {
        if (typesOn.value.length && !typesOn.value.includes(t.type)) continue
        const gk = `grp:${guid}:${dir}:${t.type}`
        const label = s.types.some((x: any) => x.type === t.type) ? t.label : 'ids, not resources'
        addNode({ key: gk, col, seq: seq++, parent: guid, group: { owner: guid, dir, type: t.type, label, count: t.count } })
        link(guid, dir, gk, [], 0)
      }
    }
    layout()
  } catch (e: any) { err.value = String(e) } finally { busy.value = false }
}

function collapse(guid: string, dir: 'in' | 'out') {
  for (const k of [...expanded]) if (k.startsWith(`${guid}:${dir}`)) expanded.delete(k)
  order = order.filter((k) => !k.startsWith(`${guid}:${dir}`))
  // drop what only this expansion brought in (recursively)
  const drop = (parent: string, side: 'in' | 'out') => {
    for (const n of [...nodes.values()]) {
      if (n.parent !== parent || n.key === p.guid) continue
      if ((side === 'out' && n.col <= nodes.get(parent)!.col) || (side === 'in' && n.col >= nodes.get(parent)!.col)) continue
      for (const d of ['in', 'out'] as const) if ([...expanded].some((k) => k.startsWith(`${n.key}:${d}`))) collapse(n.key, d)
      nodes.delete(n.key)
    }
  }
  drop(guid, dir)
  for (const [k, e] of [...edges]) if (!nodes.has(e.from) || !nodes.has(e.to)) edges.delete(k)
  layout()
}

function toggle(guid: string, dir: 'in' | 'out') {
  if ([...expanded].some((k) => k.startsWith(`${guid}:${dir}`))) collapse(guid, dir)
  else expand(guid, dir)
}

async function rebuild(keepExpansions = true) {
  const replay = keepExpansions ? order.slice() : []
  nodes.clear(); edges.clear(); expanded.clear(); order = []; seq = 0; list.value = null
  err.value = ''
  try {
    const r = await api.Node(p.guid)
    addNode({ key: p.guid, col: 0, seq: seq++, parent: '', row: r })
  } catch (e: any) { err.value = String(e); return }
  if (!replay.length) {
    await expand(p.guid, 'out')
    await expand(p.guid, 'in')
  } else {
    for (const k of replay) {
      const [g, d, t] = k.split(':')
      if (nodes.has(g)) await expand(g, d as any, t ? t.split(',').map(Number) : [])
    }
  }
  layout()
  fit()
}

function groupClick(n: GNode) {
  const g = n.group!
  sel.value = n.key
  if (g.count <= CAP) expand(g.owner, g.dir, [g.type])
  else list.value = { owner: g.owner, dir: g.dir, types: [g.type] }
}
function addFromList(r: any) {
  const l = list.value!
  const owner = nodes.get(l.owner)
  if (!owner) return
  const col = owner.col + (l.dir === 'out' ? 1 : -1)
  addNode({ key: r.guid, col, seq: seq++, parent: l.owner, row: r })
  link(l.owner, l.dir, r.guid, r.kinds, r.aux)
  layout()
  sel.value = r.guid
}

// ---- layout: columns by depth; siblings stacked around their parent; a few relaxation passes ----
const pos = reactive(new Map<string, { x: number; y: number }>())
function layout() {
  const cols = new Map<number, GNode[]>()
  for (const n of nodes.values()) { if (!cols.has(n.col)) cols.set(n.col, []); cols.get(n.col)!.push(n) }
  const ys = new Map<string, number>()
  for (const [c, ns] of cols) ns.sort((a, b) => a.seq - b.seq).forEach((n, i) => ys.set(n.key, i * ROWH + (c === 0 ? 0 : 0)))
  const keys = [...cols.keys()].sort((a, b) => Math.abs(a) - Math.abs(b) || a - b)
  for (let pass = 0; pass < 3; pass++) {
    for (const c of keys) {
      if (c === 0 && pass === 0) continue
      const ns = cols.get(c)!
      const py = (n: GNode) => (n.parent && ys.has(n.parent) ? ys.get(n.parent)! : ys.get(n.key)!)
      ns.sort((a, b) => py(a) - py(b) || a.seq - b.seq)
      let last = -Infinity
      let k = 0
      while (k < ns.length) {
        let j = k
        while (j < ns.length && ns[j].parent === ns[k].parent) j++
        const n = j - k
        let y = py(ns[k]) - ((n - 1) / 2) * ROWH
        for (let i = k; i < j; i++) {
          y = Math.max(y, last + ROWH)
          ys.set(ns[i].key, y)
          last = y
          y += ROWH
        }
        k = j
      }
    }
  }
  pos.clear()
  for (const n of nodes.values()) pos.set(n.key, { x: n.col * COLW, y: ys.get(n.key) ?? 0 })
}

// ---- view ----
const view = reactive({ x: 0, y: 0, k: 1 })
const box = ref<SVGSVGElement>()
function fit() {
  if (!pos.size || !box.value) return
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const q of pos.values()) { x0 = Math.min(x0, q.x); y0 = Math.min(y0, q.y); x1 = Math.max(x1, q.x + W); y1 = Math.max(y1, q.y + H) }
  const r = box.value.getBoundingClientRect()
  // readable first: never below 0.85 (what doesn't fit is panned, centred on the root)
  const k = Math.min(1.1, (r.width - 80) / (x1 - x0 + 60), (r.height - 40) / (y1 - y0 + 1))
  view.k = Math.max(0.85, k)
  const root = pos.get(p.guid)
  view.x = view.k * (x1 - x0 + 60) < r.width || !root ? r.width / 2 - ((x0 + x1) / 2) * view.k : r.width / 2 - (root.x + W / 2) * view.k
  view.y = view.k * (y1 - y0) < r.height ? r.height / 2 - ((y0 + y1) / 2) * view.k
    : root ? r.height / 2 - (root.y + H / 2) * view.k : 20 - y0 * view.k
}
function center(key: string) {
  const q = pos.get(key), r = box.value?.getBoundingClientRect()
  if (!q || !r) return
  view.x = r.width / 2 - (q.x + W / 2) * view.k
  view.y = r.height / 2 - (q.y + H / 2) * view.k
}
function wheel(e: WheelEvent) {
  const r = box.value!.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  const k = Math.min(2.5, Math.max(0.08, view.k * Math.exp(-e.deltaY * 0.0015)))
  view.x = mx - (mx - view.x) * (k / view.k)
  view.y = my - (my - view.y) * (k / view.k)
  view.k = k
}
let drag: { x: number; y: number } | null = null
function down(e: MouseEvent) { if (e.button === 0) drag = { x: e.clientX - view.x, y: e.clientY - view.y } }
function move(e: MouseEvent) { if (drag) { view.x = e.clientX - drag.x; view.y = e.clientY - drag.y } }
function up() { drag = null }

function path(e: GEdge) {
  const a = pos.get(e.from), b = pos.get(e.to)
  if (!a || !b) return ''
  if (a.x < b.x) {
    const x1 = a.x + W, y1 = a.y + H / 2, x2 = b.x, y2 = b.y + H / 2, dx = Math.max(40, (x2 - x1) / 2)
    return `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`
  }
  // same column or backwards (cycles): loop over the top
  const x1 = a.x + W / 2, y1 = a.y, x2 = b.x + W / 2, y2 = b.y, top = Math.min(y1, y2) - 40
  return `M${x1},${y1} C${x1},${top} ${x2},${top} ${x2},${y2}`
}
const edgeList = computed(() => [...edges.values()])
const nodeList = computed(() => [...nodes.values()])
const isExp = (g: string, d: string) => [...expanded].some((k) => k.startsWith(`${g}:${d}`))
const selNode = computed(() => nodes.get(sel.value))
const title = (r: any) => r.name || s.types.find((x: any) => x.type === r.type)?.label || 'unnamed'
const clip = (t: string, n: number) => (t.length > n ? t.slice(0, n - 1) + '…' : t)

function nodeClick(e: MouseEvent, n: GNode) {
  if (n.group) return groupClick(n)
  if (e.ctrlKey || e.metaKey) { open(n.key, { newTab: true, background: !e.shiftKey }); return }
  if (e.shiftKey) { open(n.key, { newWindow: true }); return }
  sel.value = n.key
}
function nodeOpen(n: GNode) { if (!n.group) open(n.key) }
watch(sel, async (g) => {
  why.value = null
  const n = nodes.get(g)
  if (!n || n.group || !n.parent || nodes.get(n.parent)?.group) return
  const e = edges.get(`${n.parent}>${g}`) ?? edges.get(`${g}>${n.parent}`)
  if (!e) return
  why.value = { edge: e, ...(await api.RefDetail(e.from, e.to)) }
})
function reroot() { if (selNode.value?.row) open(selNode.value.key, { tab: 'deps' }) }
const kindLabel = (k: string) => s.kinds.find((x: any) => x.name === k)?.label ?? k
function toggleKind(k: string) {
  kindsOn.value = kindsOn.value.includes(k) ? kindsOn.value.filter((x) => x !== k) : [...kindsOn.value, k]
}
const graphTypes = computed(() => {
  const m = new Map<number, number>()
  for (const n of nodes.values()) {
    const t = n.row?.type ?? n.group?.type
    if (t !== undefined && n.key !== p.guid) m.set(t, (m.get(t) ?? 0) + (n.group ? n.group.count : 1))
  }
  return [...m.entries()].sort((a, b) => b[1] - a[1])
})
function toggleType(t: number) {
  typesOn.value = typesOn.value.includes(t) ? typesOn.value.filter((x) => x !== t) : [...typesOn.value, t]
}

let ready = false
let size = { w: 0, h: 0 }
onMounted(async () => {
  // keep the view's centre when the pane resizes (wide mode, window)
  new ResizeObserver(() => {
    const r = box.value?.getBoundingClientRect()
    if (!r) return
    if (size.w) { view.x += (r.width - size.w) / 2; view.y += (r.height - size.h) / 2 }
    size = { w: r.width, h: r.height }
  }).observe(box.value!)
  if (!s.kinds.length) s.kinds = await api.Kinds()
  defaultKinds()
  await rebuild(false)
  ready = true
})
watch(() => s.status?.index?.phase, async (ph) => {
  if (ph === 'done' && ready && err.value) { s.kinds = await api.Kinds(); defaultKinds(); rebuild(false) }
})
let t: any
watch([kindsOn, typesOn, pkg, noMissing], () => { if (!ready) return; clearTimeout(t); t = setTimeout(() => rebuild(true), 250) }, { deep: true })
const sideW = usePanel('deps.side', 320)
</script>

<template>
  <div class="flex h-full min-h-0">
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <div class="flex items-center gap-1.5 overflow-x-auto border-b border-line px-3 py-1.5 text-[11.5px] whitespace-nowrap">
        <span class="text-dim">Kinds</span>
        <button v-for="k in s.kinds" :key="k.name" class="chip" :class="kindsOn.includes(k.name) ? 'chip-on' : ''" :title="k.name" @click="toggleKind(k.name)">
          <span class="h-1.5 w-1.5 rounded-full" :style="{ background: kindColor(k.name) }" />{{ k.label }}
        </button>
        <button class="chip" :class="noMissing ? '' : 'chip-on'" title="GUIDs that are not resources of the install (Statescript config-var ids, …)" @click="noMissing = !noMissing">non-resources</button>
        <input v-model="pkg" class="mono ml-2 w-[150px] rounded border border-line bg-well px-1.5 py-0.5 placeholder:font-sans placeholder:text-faint" placeholder="only in package…" title="Only neighbours listed by this package (GUID)" />
        <button class="ml-auto text-dim hover:text-fg" @click="rebuild(false)">Reset</button>
        <button class="text-dim hover:text-fg" @click="fit">Fit</button>
      </div>
      <div v-if="graphTypes.length > 1 || typesOn.length" class="flex items-center gap-1.5 overflow-x-auto border-b border-line px-3 py-1 text-[11.5px] whitespace-nowrap">
        <span class="text-dim">Types</span>
        <button v-for="[t, n] in graphTypes.slice(0, 16)" :key="t" class="chip" :class="typesOn.includes(t) ? 'chip-on' : ''" @click="toggleType(t)">
          <span class="mono" :style="{ color: typeColor(t) }">{{ hex3(t) }}</span>{{ s.types.find((x: any) => x.type === t)?.label ?? '' }} <span class="mono text-faint">{{ num(n) }}</span>
        </button>
        <button v-if="typesOn.length" class="text-faint hover:text-fg" @click="typesOn = []">all types</button>
      </div>
      <div class="relative min-h-0 flex-1 overflow-hidden bg-well">
        <svg ref="box" class="h-full w-full" @wheel.prevent="wheel" @mousedown="down" @mousemove="move" @mouseup="up" @mouseleave="up">
          <defs>
            <pattern id="dgrid" width="24" height="24" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="0.8" fill="#253049" /></pattern>
            <marker id="arr" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L8,4 L0,8 z" fill="#4a5a7a" /></marker>
          </defs>
          <rect width="100%" height="100%" fill="url(#dgrid)" />
          <g :transform="`translate(${view.x},${view.y}) scale(${view.k})`">
            <path v-for="e in edgeList" :key="e.from + '>' + e.to" :d="path(e)" fill="none" marker-end="url(#arr)"
              :stroke="e.from === sel || e.to === sel ? '#8fb4ff' : e.kinds.length ? kindColor(e.kinds[0]) : '#4a5a7a'"
              :stroke-opacity="e.from === sel || e.to === sel ? 1 : 0.55" :stroke-width="e.from === sel || e.to === sel ? 2 : 1.2" />
            <g v-for="n in nodeList" :key="n.key" :transform="`translate(${pos.get(n.key)?.x ?? 0},${pos.get(n.key)?.y ?? 0})`"
              @mousedown.stop @click="nodeClick($event, n)" @dblclick="nodeOpen(n)" @auxclick="(e: MouseEvent) => e.button === 1 && !n.group && open(n.key, { newTab: true, background: true })"
              @contextmenu.prevent="!n.group && (s.menu = { x: $event.clientX, y: $event.clientY, guid: n.key, pane })">
              <template v-if="n.group">
                <rect :width="W" :height="H" rx="8" fill="#151d2b" :stroke="sel === n.key ? '#8fb4ff' : '#34425f'" stroke-dasharray="5 4" />
                <rect x="0" y="0" :width="4" :height="H" rx="2" :fill="typeColor(n.group.type)" />
                <text x="14" y="21" fill="#d8e0ec" font-size="13" font-family="Instrument Sans Variable">{{ num(n.group.count) }} × {{ clip(n.group.label, 22) }}</text>
                <text x="14" y="39" fill="#8a98b0" font-size="11" font-family="Instrument Sans Variable"><tspan font-family="JetBrains Mono Variable" :fill="typeColor(n.group.type)">{{ hex3(n.group.type) }}</tspan> · {{ n.group.count <= CAP ? 'click to expand' : 'click to list' }}</text>
              </template>
              <template v-else>
                <rect :width="W" :height="H" rx="8" :fill="n.key === p.guid ? '#1f2d47' : sel === n.key ? '#24406b' : '#151d2b'"
                  :stroke="sel === n.key ? '#8fb4ff' : n.key === p.guid ? '#8fb4ff88' : '#34425f'" :stroke-dasharray="n.row.missing ? '3 3' : ''" :opacity="n.row.missing ? 0.6 : 1" />
                <rect x="0" y="0" :width="4" :height="H" rx="2" :fill="typeColor(n.row.type)" />
                <text x="14" y="20" :fill="n.row.name ? '#d8e0ec' : '#8a98b0'" font-size="12.5" font-family="Instrument Sans Variable" :font-style="n.row.guess ? 'italic' : ''">{{ clip(title(n.row), 30) }}</text>
                <text x="14" y="38" fill="#5b6780" font-size="10.5" font-family="JetBrains Mono Variable"><tspan :fill="typeColor(n.row.type)">{{ n.row.guid.slice(0, 4) }}</tspan>{{ n.row.guid.slice(4).replace(/^0+(?=.)/, '…') }}<tspan fill="#8a98b0" font-family="Instrument Sans Variable"> {{ n.row.note ? clip(n.row.note, 18) : (n.row.missing ? 'not a resource' : '') }}</tspan></text>
                <!-- handles: ◂ used by (left), uses ▸ (right) -->
                <g v-if="n.row.in" class="cursor-pointer" @click.stop="toggle(n.key, 'in')" @mousedown.stop>
                  <rect x="-30" :y="H / 2 - 10" width="30" height="20" rx="10" :fill="isExp(n.key, 'in') ? '#24406b' : '#1c2638'" stroke="#34425f" />
                  <text x="-15" :y="H / 2 + 4" text-anchor="middle" fill="#a9c3ff" font-size="10" font-family="JetBrains Mono Variable">{{ n.row.in > 999 ? Math.round(n.row.in / 1000) + 'k' : n.row.in }}</text>
                </g>
                <g v-if="n.row.out" class="cursor-pointer" @click.stop="toggle(n.key, 'out')" @mousedown.stop>
                  <rect :x="W" :y="H / 2 - 10" width="30" height="20" rx="10" :fill="isExp(n.key, 'out') ? '#24406b' : '#1c2638'" stroke="#34425f" />
                  <text :x="W + 15" :y="H / 2 + 4" text-anchor="middle" fill="#a9c3ff" font-size="10" font-family="JetBrains Mono Variable">{{ n.row.out > 999 ? Math.round(n.row.out / 1000) + 'k' : n.row.out }}</text>
                </g>
              </template>
            </g>
          </g>
        </svg>
        <div v-if="busy" class="pointer-events-none absolute top-2 left-3 rounded bg-panel/90 px-2 py-0.5 text-[11px] text-dim">loading…</div>
        <div v-if="err" class="absolute top-2 left-3 rounded bg-panel/90 px-2 py-0.5 text-[12px] text-v-err">{{ err }}</div>
        <p class="pointer-events-none absolute bottom-2 left-3 rounded bg-panel/90 px-2 py-0.5 text-[11px] text-faint">
          Left: used by · right: uses. Click the counts to expand / collapse, a node to select, double-click to open, Ctrl-click for a new tab. Drag to pan, scroll to zoom.
        </p>
      </div>
    </div>

    <Splitter id="deps.side" :def="320" :min="220" :max="900" side="after" />
    <aside class="flex min-h-0 shrink-0 flex-col border-l border-line" :style="{ width: sideW + 'px' }">
      <template v-if="list">
        <div class="flex items-center gap-2 border-b border-line px-3 py-1.5">
          <span class="font-medium">{{ list.dir === 'out' ? 'Uses' : 'Used by' }}</span>
          <Guid :guid="list.owner" />
          <button class="ml-auto text-faint hover:text-fg" title="Close the list" @click="list = null">×</button>
        </div>
        <NeighborList class="min-h-0 flex-1" :guid="list.owner" :dir="list.dir" :types="list.types" :kinds="kindsOn" addable compact :no-missing="noMissing" @add="addFromList" />
      </template>
      <div v-else-if="selNode && selNode.row" class="selectable min-h-0 overflow-auto p-4 text-[12.5px]">
        <div class="flex items-center gap-2"><span class="h-2.5 w-2.5 rounded-[3px]" :style="{ background: typeColor(selNode.row.type) }" /><span class="mono" :style="{ color: typeColor(selNode.row.type) }">{{ hex3(selNode.row.type) }}</span><span class="text-dim">{{ s.types.find((x: any) => x.type === selNode!.row.type)?.label }}</span></div>
        <h3 class="mt-1 text-[15px] leading-snug break-words" :class="selNode.row.guess ? 'italic' : ''">{{ selNode.row.name || 'unnamed' }}</h3>
        <p v-if="selNode.row.note" class="text-dim">{{ selNode.row.note }}</p>
        <p class="mt-1"><Guid :guid="selNode.row.guid" :missing="selNode.row.missing" /></p>
        <div v-if="!selNode.row.missing" class="mt-1"><OpenIn :guid="selNode.row.guid" :ask="{ kind: 'resource', guid: selNode.row.guid, label: selNode.row.name || selNode.row.guid }" /></div>
        <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[12px]">
          <dt class="text-dim">Uses</dt><dd class="mono">{{ num(selNode.row.out) }}</dd>
          <dt class="text-dim">Used by</dt><dd class="mono">{{ num(selNode.row.in) }}</dd>
          <dt class="text-dim">Size</dt><dd class="mono">{{ selNode.row.missing ? 'not a resource of the install' : num(selNode.row.size) + ' bytes' }}</dd>
        </dl>
        <div class="mt-3 flex flex-wrap gap-1.5 text-[12px]">
          <button class="btn" :disabled="selNode.row.missing" @click="open(selNode!.key)">Open</button>
          <button class="btn" :disabled="selNode.row.missing" @click="open(selNode!.key, { newTab: true })">New tab</button>
          <button v-if="selNode.key !== p.guid" class="btn" @click="reroot">Graph from here</button>
          <button v-if="selNode.row.in" class="btn" @click="list = { owner: selNode!.key, dir: 'in', types: [] }">List used by</button>
          <button v-if="selNode.row.out" class="btn" @click="list = { owner: selNode!.key, dir: 'out', types: [] }">List uses</button>
          <button class="btn" @click="center(selNode!.key)">Center</button>
        </div>
        <template v-if="why">
          <h4 class="mt-4 mb-1 font-medium">Link to <Guid :guid="selNode.parent" /></h4>
          <p class="text-[12px] text-dim">
            <Guid :guid="why.edge.from" :link="false" /> uses <Guid :guid="why.edge.to" :link="false" /> as
            {{ why.kinds.map(kindLabel).join(', ') }}
          </p>
          <p v-if="why.aux" class="mt-1">
            <button class="text-focus hover:underline" @click="open(why.edge.from, { tab: 'map', instance: why.aux - 1 })">Open the map at record {{ why.aux - 1 }}</button>
          </p>
          <p v-for="(x, i) in why.paths" :key="i" class="mono mt-0.5 text-[11px] break-all text-v-num">{{ x }}</p>
        </template>
      </div>
      <p v-else class="p-4 text-dim">Select a node to see what it is and why it is linked; open a group or “List …” for the full neighbour list.</p>
    </aside>
  </div>
</template>

<style scoped>
.chip { flex-shrink: 0; display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--color-line); border-radius: 999px; padding: 0 8px; line-height: 20px; color: var(--color-dim); }
.chip:hover { color: var(--color-fg); border-color: var(--color-line-strong); }
.chip-on { background: var(--color-sel); color: var(--color-fg); border-color: var(--color-focus); }
.btn { border: 1px solid var(--color-line); border-radius: 5px; padding: 2px 8px; color: var(--color-fg); }
.btn:hover:not(:disabled) { background: var(--color-raised); }
.btn:disabled { color: var(--color-faint); }
</style>
