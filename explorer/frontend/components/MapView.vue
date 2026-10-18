<script setup lang="ts">
// Map data (type 0x002, or a map header 0x09F → its m_map): top-down plot of every placement, the placement
// list, referenced assets and the selected record's fields and instance data (docs/Explorer.md "Map view").
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'
import { bytes, hex3, num, typeColor } from '~/utils/format'
import { usePanel } from '~/composables/usePanel'
import { WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from '~/wailsjs/runtime/runtime'

// A window opened by the 3D view's "pop out" (--map3d) starts in 3D, maximised; only its first map view does.
let startupMap3D: Promise<boolean> | null = null

const p = defineProps<{ guid: string }>()
const v = ref<any>(null)
const err = ref('')
const loading = ref(true)
const side = ref<'placements' | 'assets' | 'info'>('placements')
const filter = ref('')
const sel = ref(-1)
const selInst = ref<any>(null) // a model instance picked in 3D (T064): { record, instance, model, look, pos }
watch(sel, (i) => { if (selInst.value && selInst.value.record !== i) selInst.value = null })
const detail = ref<any>(null)
const { s: ex, pane } = useExplorer() // ow://<map>/map/<record index> selects a record

// Classes (mapview.classify): colour, plot size, shown by default.
const CLS: Record<string, { c: string; r: number; on: boolean; label: string }> = {
  networked: { c: '#ff8a78', r: 4, on: true, label: 'networked entity (server creates it)' },
  mover: { c: '#e8c46b', r: 3.5, on: true, label: 'mover (movement state)' },
  gameplay: { c: '#8fd694', r: 2.5, on: true, label: 'gameplay entity' },
  prop: { c: '#7f8ba3', r: 1.6, on: true, label: 'prop entity' },
  static: { c: '#3d4a66', r: 1.1, on: true, label: 'static model / model group' },
  placed_model: { c: '#8fb4ff', r: 1.8, on: true, label: 'placed model' },
  light: { c: '#ffd98a', r: 1.6, on: false, label: 'light' },
  effect: { c: '#c9b6ff', r: 1.6, on: false, label: 'effect' },
  sound_emitter: { c: '#7fd6c2', r: 2, on: false, label: 'sound emitter' },
  sound_volume: { c: '#4fb3a0', r: 2, on: false, label: 'sound volume' },
  occluder: { c: '#5b6780', r: 1.2, on: false, label: 'occluder' },
  reflection_probe: { c: '#9aa7c0', r: 1.4, on: false, label: 'reflection probe' },
  text_label: { c: '#eaa9c6', r: 2, on: false, label: 'text label' },
  map_text: { c: '#eaa9c6', r: 2, on: false, label: 'map text' },
  'volume_0x44': { c: '#a9c3ff', r: 2, on: false, label: 'volume 0x44' },
  'volume_0x45': { c: '#a9c3ff', r: 2, on: false, label: 'volume 0x45' },
}
const shown = reactive<Record<string, boolean>>(Object.fromEntries(Object.entries(CLS).map(([k, x]) => [k, x.on])))
const cls = (k: string) => CLS[k] ?? { c: '#8a98b0', r: 1.5, on: true, label: k }

onMounted(async () => {
  try {
    startupMap3D ??= api.StartupMap3D().catch(() => false)
    const pop = await startupMap3D
    startupMap3D = Promise.resolve(false)
    if (pop) { is3d.value = 1; ex.wide = true; ex.mapMax = pane.id } // not committed: the parent's setting stays
    v.value = await api.MapView(p.guid)
    loading.value = false
    await nextTick()
    if (!is3d.value) fit()
    if (ex.instance >= 0) focus(ex.instance)
  } catch (e: any) { err.value = String(e) } finally { loading.value = false }
})

const TYPE_NAMES = ['type 0', 'model group', 'model', 'occluder', 'reflection probe', 'type 5', 'text label', 'map text',
  'placed model', 'light', 'sound volume', 'entity', 'sound emitter', 'effect', 'volume 0x44', 'volume 0x45']

const list = computed(() => {
  if (!v.value) return []
  const f = filter.value.trim().toLowerCase()
  const out = v.value.placements.filter((x: any) => shown[x.class] !== false && (!f ||
    (x.name ?? '').toLowerCase().includes(f) || (x.guid ?? '').includes(f) || x.class.includes(f) ||
    TYPE_NAMES[x.type].includes(f) || (x.placed >= 0 && x.placed.toString(16) === f.replace(/^0x/, '')) ||
    (x.overrides ?? []).some((o: string) => o.toLowerCase().includes(f))))
  const pri = ['networked', 'mover', 'gameplay', 'prop', 'placed_model']
  const rank = (c: string) => { const i = pri.indexOf(c); return i < 0 ? pri.length : i }
  return out.slice().sort((a: any, b: any) => rank(a.class) - rank(b.class) || a.i - b.i)
})
const LIMIT = 1500
const legend = ref(true)

watch(() => ex.instance, (i) => { if (v.value && i >= 0) focus(i) })
// the selection card over the plot: what the selected placement stands for (App.PlacementBrief: primary resource,
// definition graphs), open it elsewhere, ask Claude about it
const card = ref<any>(null)
const cardOpen = ref(true)
watch(sel, async (i) => {
  card.value = null
  if (i < 0) return
  try { const b = await api.PlacementBrief(p.guid, i); if (sel.value === i) card.value = b } catch (e: any) { card.value = { error: String(e) } }
})
const mapName = computed(() => (typeof ex.resource?.name === 'object' ? ex.resource?.name?.text : ex.resource?.name) || p.guid)
const cardAsk = computed(() => ({ kind: 'placement' as const, map: p.guid, record: sel.value,
  ...(selInst.value ? { instance: selInst.value.instance } : {}),
  label: `${card.value?.name || card.value?.recordType || 'record'} #${sel.value}${selInst.value && card.value?.model?.instances > 1 ? ` instance ${selInst.value.instance}` : ''} on ${mapName.value}` }))
// the look shown in the card: the picked instance's, else the record's first
const cardLook = computed(() => {
  const ls = card.value?.model?.looks ?? []
  return ls.find((l: any) => l.look === selInst.value?.look) ?? ls[0] ?? null
})
// the Ask panel follows the selected placement (useAsk liveSubject)
watch([sel, card], () => {
  if (sel.value >= 0 && card.value && !card.value.error) ex.extra.ask = { tab: 'map', subject: cardAsk.value }
  else if (ex.extra.ask?.tab === 'map') delete ex.extra.ask
})
onBeforeUnmount(() => { if (ex.extra.ask?.tab === 'map') delete ex.extra.ask })
watch(sel, async (i) => {
  detail.value = null
  if (i < 0) return
  try { detail.value = await api.MapRecord(p.guid, i) } catch (e: any) { detail.value = { error: String(e) } }
})

// 2D plot or 3D mode (Map3D.vue: collision mesh + markers), remembered with the panel sizes
const is3d = usePanel('map.3d', 0)
const m3d = ref<any>()

// Maximise: the plot takes the whole window above the status bar (F; Esc returns); F11 also makes the window
// fullscreen. Pop out: this map's 3D view in its own window (App.OpenMap3DWindow → --map3d).
const maxed = computed(() => ex.mapMax === pane.id)
const fullscreen = ref(false)
function toggleMax() {
  if (maxed.value) exitMax()
  else ex.mapMax = pane.id
}
async function exitMax() {
  ex.mapMax = 0
  if (fullscreen.value) { WindowUnfullscreen(); fullscreen.value = false }
}
async function toggleFullscreen() {
  const on = await WindowIsFullscreen().catch(() => fullscreen.value)
  if (on) { WindowUnfullscreen(); fullscreen.value = false }
  else { ex.mapMax = pane.id; WindowFullscreen(); fullscreen.value = true }
}
function popOut() {
  api.OpenMap3DWindow(p.guid, sel.value).catch((e: any) => { err.value = `Pop out failed: ${e}` })
}
onBeforeUnmount(() => { if (maxed.value) exitMax() })
function setMode(m: number) {
  if (m === is3d.value) return
  is3d.value = m
  is3d.commit()
  if (!m) nextTick(() => { fit(); if (sel.value >= 0) focus(sel.value) })
}

// ---- plot (x → right, z → down; world Y is up; H / V mirror the view, world coordinates stay as they are) ----
const flipH = usePanel('map.flipH', 0) // stored with the panel sizes: 1 = mirrored
const flipV = usePanel('map.flipV', 0)
const FX = () => (flipH.value ? -1 : 1), FZ = () => (flipV.value ? -1 : 1)
const sx = (x: number) => view.x + FX() * x * view.k // world x → screen
const sy = (z: number) => view.y + FZ() * z * view.k // world z → screen
function flip(axis: 'h' | 'v') {
  if (is3d.value) { // 3D: the world is mirrored when exactly one of the two is set (Map3D mirror)
    const f = axis === 'h' ? flipH : flipV
    f.value = f.value ? 0 : 1
    f.commit(!f.value)
    return
  }
  // keep the world point at the centre of the plot where it is
  const r = wrap.value?.getBoundingClientRect()
  const cx = r ? r.width / 2 : 0, cy = r ? r.height / 2 : 0
  const wx = (cx - view.x) / (FX() * view.k), wz = (cy - view.y) / (FZ() * view.k)
  const f = axis === 'h' ? flipH : flipV
  f.value = f.value ? 0 : 1
  f.commit(!f.value)
  view.x = cx - FX() * wx * view.k
  view.y = cy - FZ() * wz * view.k
  draw()
}
function flipKey(e: KeyboardEvent) {
  const t = (e.target as HTMLElement)?.tagName
  if (t === 'INPUT' || t === 'TEXTAREA' || e.ctrlKey || e.metaKey || e.altKey || ex.focus !== pane) return
  if (e.key === 'Escape' && maxed.value) { e.preventDefault(); exitMax(); return }
  if (e.key === 'F11') { e.preventDefault(); toggleFullscreen(); return }
  if (e.key === 'f' || e.key === 'F') { toggleMax(); return }
  if (e.key === 'h' || e.key === 'H') flip('h')
  else if (e.key === 'v' || e.key === 'V') flip('v')
}
onMounted(() => window.addEventListener('keydown', flipKey))
onBeforeUnmount(() => window.removeEventListener('keydown', flipKey))
const cv = ref<HTMLCanvasElement>()
const wrap = ref<HTMLDivElement>()
const view = reactive({ x: 0, y: 0, k: 1 })
const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => draw()) : null
watch(() => wrap.value, (w, old) => { if (old) ro?.unobserve(old); if (w) ro?.observe(w) })
onBeforeUnmount(() => ro?.disconnect())
function extent() {
  const ps = (v.value?.placements ?? []).filter((x: any) => x.hasPos && x.type === 11)
  if (!ps.length) return { x0: -100, z0: -100, x1: 100, z1: 100 }
  const xs = ps.map((x: any) => x.pos[0]).sort((a: number, b: number) => a - b)
  const zs = ps.map((x: any) => x.pos[2]).sort((a: number, b: number) => a - b)
  const q = (a: number[], t: number) => a[Math.floor(t * (a.length - 1))]
  return { x0: q(xs, 0.01), x1: q(xs, 0.99), z0: q(zs, 0.01), z1: q(zs, 0.99) }
}
function fit() {
  const e = extent()
  const r = wrap.value?.getBoundingClientRect() ?? { width: 800, height: 600 }
  const k = Math.min(r.width / (e.x1 - e.x0 + 20), r.height / (e.z1 - e.z0 + 20))
  view.k = k
  view.x = r.width / 2 - FX() * ((e.x0 + e.x1) / 2) * k
  view.y = r.height / 2 - FZ() * ((e.z0 + e.z1) / 2) * k
  draw()
}
function draw() {
  const c = cv.value, w = wrap.value
  if (!c || !w || !v.value) return
  const r = w.getBoundingClientRect(), dpr = window.devicePixelRatio || 1
  c.width = r.width * dpr
  c.height = r.height * dpr
  c.style.width = r.width + 'px'
  c.style.height = r.height + 'px'
  const g = c.getContext('2d')!
  g.setTransform(dpr, 0, 0, dpr, 0, 0)
  g.fillStyle = '#0e131e'
  g.fillRect(0, 0, r.width, r.height)
  // grid every 50 units
  g.strokeStyle = '#1a2233'
  g.lineWidth = 1
  const step = 50 * view.k
  if (step > 12) {
    for (let x = view.x % step; x < r.width; x += step) { g.beginPath(); g.moveTo(x, 0); g.lineTo(x, r.height); g.stroke() }
    for (let y = view.y % step; y < r.height; y += step) { g.beginPath(); g.moveTo(0, y); g.lineTo(r.width, y); g.stroke() }
  }
  const P = v.value.placements
  const X = sx, Y = sy
  const order = ['static', 'occluder', 'reflection_probe', 'light', 'effect', 'sound_volume', 'sound_emitter', 'placed_model',
    'prop', 'volume_0x44', 'volume_0x45', 'text_label', 'map_text', 'gameplay', 'mover', 'networked']
  if (shown.static) {
    g.fillStyle = CLS.static.c
    for (const q of v.value.points) g.fillRect(X(q.x) - 0.5, Y(q.z) - 0.5, 1.2, 1.2)
  }
  for (const k of order) {
    if (!shown[k]) continue
    const s = cls(k)
    g.fillStyle = s.c
    for (const x of P) {
      if (x.class !== k || !x.hasPos) continue
      const px = X(x.pos[0]), py = Y(x.pos[2])
      if (px < -5 || py < -5 || px > r.width + 5 || py > r.height + 5) continue
      g.beginPath()
      g.arc(px, py, s.r, 0, Math.PI * 2)
      g.fill()
      if (k === 'networked' || k === 'mover') { // facing
        g.strokeStyle = s.c
        g.beginPath(); g.moveTo(px, py); g.lineTo(px + FX() * Math.sin(x.yaw) * 9, py + FZ() * Math.cos(x.yaw) * 9); g.stroke()
      }
    }
  }
  if (sel.value >= 0) {
    const x = P[sel.value]
    if (x?.hasPos) {
      g.strokeStyle = '#ffffff'
      g.lineWidth = 2
      g.beginPath(); g.arc(X(x.pos[0]), Y(x.pos[2]), 8, 0, Math.PI * 2); g.stroke()
    }
  }
  origin(g, X(0), Y(0), r.width, r.height)
  // scale bar
  g.fillStyle = '#8a98b0'
  g.font = '11px JetBrains Mono Variable, monospace'
  const len = 50 * view.k
  g.fillRect(12, r.height - 18, len, 2)
  g.fillText('50 m', 16 + len, r.height - 13)
}
watch([() => ({ ...shown }), sel, () => view.k, flipH, flipV], draw, { deep: true })

// The world origin (0, 0, 0) as an axes gizmo: +X right (red), +Z down (blue), +Y up out of the plot (green).
// Off screen, an arrow on the edge points at it.
function origin(g: CanvasRenderingContext2D, ox: number, oy: number, w: number, h: number) {
  const L = 42
  g.lineWidth = 2
  g.font = '11px JetBrains Mono Variable, monospace'
  if (ox < 0 || oy < 0 || ox > w || oy > h) {
    const cx = Math.min(w - 14, Math.max(14, ox)), cy = Math.min(h - 14, Math.max(14, oy))
    const a = Math.atan2(oy - cy, ox - cx)
    g.fillStyle = '#e8c46b'
    g.beginPath()
    g.moveTo(cx + Math.cos(a) * 9, cy + Math.sin(a) * 9)
    g.lineTo(cx + Math.cos(a + 2.5) * 7, cy + Math.sin(a + 2.5) * 7)
    g.lineTo(cx + Math.cos(a - 2.5) * 7, cy + Math.sin(a - 2.5) * 7)
    g.fill()
    return
  }
  const arrow = (dx: number, dy: number, c: string, t: string) => {
    g.strokeStyle = c; g.fillStyle = c
    g.beginPath(); g.moveTo(ox, oy); g.lineTo(ox + dx * L, oy + dy * L); g.stroke()
    g.beginPath()
    g.moveTo(ox + dx * (L + 7), oy + dy * (L + 7))
    g.lineTo(ox + dx * L - dy * 4, oy + dy * L + dx * 4)
    g.lineTo(ox + dx * L + dy * 4, oy + dy * L - dx * 4)
    g.fill()
    g.fillText(t, ox + dx * (L + 12) - 3, oy + dy * (L + 12) + 4)
  }
  arrow(FX(), 0, '#ff6b6b', 'X')
  arrow(0, FZ(), '#6ba6ff', 'Z')
  g.strokeStyle = '#7fd68a'; g.fillStyle = '#7fd68a'
  g.beginPath(); g.arc(ox, oy, 6, 0, Math.PI * 2); g.stroke()
  g.beginPath(); g.arc(ox, oy, 1.8, 0, Math.PI * 2); g.fill()
  g.fillText('Y', ox - 16, oy - 8)
  g.fillStyle = '#d8e0ec'
  g.fillText('0, 0, 0', ox + 8, oy - 8)
}

// World coordinates under the cursor (status bar): x and z from the plot; y (height) only on a placement.
function hover(e: MouseEvent) {
  if (!v.value || !wrap.value) return
  const r = wrap.value.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  const wx = (mx - view.x) / (FX() * view.k), wz = (my - view.y) / (FZ() * view.k)
  let best: any = null, bd = 8 * 8
  for (const x of v.value.placements) {
    if (!x.hasPos || !shown[x.class]) continue
    const dx = sx(x.pos[0]) - mx, dy = sy(x.pos[2]) - my
    const d = dx * dx + dy * dy
    if (d < bd) { bd = d; best = x }
  }
  const f = (n: number) => n.toFixed(2)
  ex.mapCursor = `x ${f(wx)}  z ${f(wz)}` + (best ? `   ·   ${best.name || TYPE_NAMES[best.type]} at (${best.pos.map(f).join(', ')})` : '   (y: hover a placement)')
}
function wheel(e: WheelEvent) {
  const r = wrap.value!.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  const f = Math.exp(-e.deltaY * 0.0015)
  view.x = mx - (mx - view.x) * f
  view.y = my - (my - view.y) * f
  view.k *= f
  draw()
}
let drag: { x: number; y: number; moved: boolean } | null = null
function down(e: MouseEvent) { drag = { x: e.clientX - view.x, y: e.clientY - view.y, moved: false } }
function move(e: MouseEvent) {
  hover(e)
  if (!drag) return
  const nx = e.clientX - drag.x, ny = e.clientY - drag.y
  if (Math.abs(nx - view.x) + Math.abs(ny - view.y) > 2) drag.moved = true
  view.x = nx; view.y = ny
  draw()
}
function up(e: MouseEvent) {
  if (drag && !drag.moved) pick(e)
  drag = null
}
function pick(e: MouseEvent) {
  const r = wrap.value!.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  let best = -1, bd = 10 * 10
  for (const x of v.value.placements) {
    if (!x.hasPos || !shown[x.class]) continue
    const dx = sx(x.pos[0]) - mx, dy = sy(x.pos[2]) - my
    const d = dx * dx + dy * dy
    // prefer the more important classes on ties
    const w = x.class === 'networked' || x.class === 'mover' ? d * 0.5 : d
    if (w < bd) { bd = w; best = x.i }
  }
  if (best >= 0) sel.value = best
}
function focus(i: number) {
  sel.value = i
  if (is3d.value) { m3d.value?.focus(i); return }
  const x = v.value.placements[i]
  if (!x?.hasPos) return
  const r = wrap.value!.getBoundingClientRect()
  view.x = r.width / 2 - FX() * x.pos[0] * view.k
  view.y = r.height / 2 - FZ() * x.pos[2] * view.k
  draw()
}
const fmt = (a: number[]) => a.map((x) => x.toFixed(2)).join(', ')
const sideW = usePanel('map.side', 400)
const listH = usePanel('map.list', 300)
onBeforeUnmount(() => { ex.mapCursor = '' })

// MCP current_view: the map's own state (the selected record, 2D / 3D, the 2D plot's centre and zoom; Map3D adds the
// camera and the point under the cursor)
ex.extra.map = { record: -1, mode: '2d' }
watch([sel, selInst, is3d, maxed, () => view.k, () => view.x, () => view.y, flipH, flipV], () => {
  const m = ex.extra.map
  if (!m) return
  m.record = sel.value
  m.modelInstance = selInst.value ? { instance: selInst.value.instance, model: selInst.value.model, look: selInst.value.look, pos: selInst.value.pos } : undefined
  m.mode = is3d.value ? '3d' : '2d'
  m.maximised = maxed.value
  m.mirrored = !!flipH.value !== !!flipV.value
  if (!is3d.value) {
    const r = wrap.value?.getBoundingClientRect()
    if (r) m.plot2d = { centreX: +((r.width / 2 - view.x) / (FX() * view.k)).toFixed(2), centreZ: +((r.height / 2 - view.y) / (FZ() * view.k)).toFixed(2), pxPerMetre: +view.k.toFixed(3) }
    delete m.camera
    delete m.aim
  } else delete m.plot2d
}, { immediate: true })
onBeforeUnmount(() => { delete ex.extra.map })
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else-if="loading" class="p-6 text-dim">Parsing the map data…</div>
  <div v-else-if="v" class="flex h-full min-h-0">
    <div class="relative flex min-h-0 min-w-0 flex-1 flex-col border-r border-line">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-4 py-2 text-[12px]">
        <span class="mono">{{ bytes(v.size) }}</span>
        <span>{{ num(v.placements.length) }} records <span class="text-faint">({{ num(v.placed) }} placed)</span></span>
        <span class="text-dim">collision: 1 mesh of {{ num(v.collision.meshes?.[0]?.tris ?? 0) }} triangles, {{ v.collision.hulls }} hulls, {{ v.collision.capsules }} capsules, {{ v.collision.spheres }} spheres</span>
        <span class="text-dim">nav {{ bytes(v.navSize) }}</span>
        <span class="ml-auto flex overflow-hidden rounded border border-line">
          <button v-for="(m, k) in ['2D', '3D']" :key="m" class="px-2 py-0.5 hover:bg-raised hover:text-fg" :class="is3d === k ? 'bg-sel text-fg' : 'text-dim'" :title="k ? '3D: the collision mesh and shapes with the placements' : 'Top-down plot of the placements'" @click="setMode(k)">{{ m }}</button>
        </span>
        <template v-if="!is3d">
        <button class="rounded px-2 py-0.5 hover:bg-raised hover:text-fg" :class="flipH ? 'bg-sel text-fg' : 'text-dim'" title="Mirror the view left ↔ right (H); coordinates are unchanged" @click="flip('h')">⇆ Flip H</button>
        <button class="rounded px-2 py-0.5 hover:bg-raised hover:text-fg" :class="flipV ? 'bg-sel text-fg' : 'text-dim'" title="Mirror the view top ↔ bottom (V); coordinates are unchanged" @click="flip('v')">⇅ Flip V</button>
        <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="fit">Fit</button>
        </template>
        <button v-else class="rounded px-2 py-0.5 hover:bg-raised hover:text-fg" :class="!!flipH !== !!flipV ? 'bg-sel text-fg' : 'text-dim'" title="Mirror the world's x axis (H; the same setting as the 2D flips: mirrored when exactly one of Flip H / Flip V is on). Coordinates are unchanged." @click="flip('h')">⇆ Mirror</button>
        <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Use the whole window (F; Esc returns; F11 fullscreen)" @click="toggleMax">⤢ Maximise</button>
        <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Open this map's 3D view in its own window, maximised" @click="popOut">⧉ Pop out</button>
      </div>
      <div :class="maxed ? 'fixed inset-x-0 top-0 bottom-7 z-40 bg-ink' : 'relative min-h-0 flex-1'">
      <div v-if="maxed" class="absolute top-2 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1 rounded bg-panel/90 px-2 py-1 text-[12px] whitespace-nowrap" @mousedown.stop @mouseup.stop @wheel.stop @dblclick.stop>
        <span class="max-w-60 truncate px-1 text-fg">{{ (typeof ex.resource?.name === 'object' ? ex.resource?.name?.text : ex.resource?.name) || guid }}</span>
        <span class="flex overflow-hidden rounded border border-line">
          <button v-for="(m, k) in ['2D', '3D']" :key="m" class="px-2 py-0.5 hover:bg-raised hover:text-fg" :class="is3d === k ? 'bg-sel text-fg' : 'text-dim'" @click="setMode(k)">{{ m }}</button>
        </span>
        <button class="rounded px-2 py-0.5 hover:bg-raised hover:text-fg" :class="!!flipH !== !!flipV ? 'bg-sel text-fg' : 'text-dim'" title="Mirror (H)" @click="flip('h')">⇆</button>
        <button class="rounded px-2 py-0.5 hover:bg-raised hover:text-fg" :class="fullscreen ? 'bg-sel text-fg' : 'text-dim'" title="Fullscreen window (F11)" @click="toggleFullscreen">⛶ Fullscreen</button>
        <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Open this map's 3D view in its own window" @click="popOut">⧉</button>
        <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Back to the normal layout (Esc / F)" @click="exitMax">✕ Restore</button>
      </div>
      <Map3D v-if="is3d" ref="m3d" :guid="guid" :v="v" :shown="shown" :cls="cls" :sel="sel" :mirror="!!flipH !== !!flipV" :type-names="TYPE_NAMES" @select="(i: number, inst?: any) => { selInst = inst ?? null; sel = i }" :sel-inst="selInst" />
      <div v-else ref="wrap" class="absolute inset-0 cursor-crosshair" @wheel.prevent="wheel" @mousedown="down" @mousemove="move" @mouseup="up" @mouseleave="drag = null; ex.mapCursor = ''">
        <canvas ref="cv" class="absolute inset-0" />
        <p class="pointer-events-none absolute right-3 bottom-7 rounded bg-panel/90 px-2 py-0.5 text-[11px] text-faint">Top-down: x → {{ flipH ? 'left' : 'right' }}, z → {{ flipV ? 'up' : 'down' }}{{ flipH || flipV ? ' (mirrored view)' : '' }}. Drag to pan, scroll to zoom, click to select. Lines show the facing (yaw) of networked entities and movers.</p>
      </div>
        <div v-if="sel >= 0 && card" class="absolute bottom-12 left-2 z-10 max-w-[min(560px,calc(100%-16px))] rounded-md border border-line bg-panel/95 px-3 py-2 text-[12px] shadow-xl shadow-black/40" @mousedown.stop @mouseup.stop @wheel.stop @dblclick.stop @click.stop>
          <div v-if="card.error" class="text-v-err">{{ card.error }}</div>
          <template v-else>
            <div class="flex items-center gap-2">
              <span class="inline-block h-2 w-2 shrink-0 rounded-full" :style="{ background: cls(card.class).c }" />
              <span class="min-w-0 truncate font-medium">{{ card.name || (card.recordType + (card.guid ? ' ' + card.guid.slice(4).replace(/^0+/, '') : '')) }}</span>
              <span class="shrink-0 text-faint">{{ card.class }} · #{{ sel }}{{ card.placed ? ' · placed ' + card.placed : '' }}</span>
              <button class="ml-auto shrink-0 text-faint hover:text-fg" :title="cardOpen ? 'Collapse' : 'Expand'" @click="cardOpen = !cardOpen">{{ cardOpen ? '▾' : '▸' }}</button>
            </div>
            <template v-if="cardOpen">
              <div v-if="card.guid" class="mt-1 flex flex-wrap items-center gap-x-2"><span class="w-16 shrink-0 text-dim">{{ card.recordType === 'entity' ? 'definition' : 'resource' }}</span><Guid :guid="card.guid" /><OpenIn :guid="card.guid" :tab="card.recordType === 'entity' ? 'entity' : card.model ? 'model' : undefined" /></div>
              <div v-for="gl in card.definition?.graphs ?? []" :key="gl" class="flex flex-wrap items-center gap-x-2"><span class="w-16 shrink-0 text-dim">graph</span><Guid :guid="gl.split(' ')[0]" tab="graph" /><span v-if="gl.includes(' ')" class="text-dim">{{ gl.split(' ').slice(1).join(' ') }}</span><OpenIn :guid="gl.split(' ')[0]" tab="graph" /></div>
              <div v-if="card.definition?.components" class="mt-0.5 truncate text-faint" :title="card.definition.components.join(', ')">{{ card.definition.components.join(' · ') }}</div>
              <template v-if="card.model">
                <div v-if="card.model.instances > 1 || selInst" class="mt-0.5 text-dim">
                  <template v-if="selInst">instance {{ selInst.instance }} of {{ card.model.instances }} at <span class="mono">({{ selInst.pos.join(', ') }})</span></template>
                  <template v-else>{{ card.model.instances }} instances ({{ card.model.looks.length }} look{{ card.model.looks.length === 1 ? '' : 's' }})</template>
                </div>
                <div v-if="is3d" class="mt-0.5 flex flex-wrap items-center gap-1">
                  <button class="rounded border border-line px-1.5 text-dim hover:text-fg" :title="selInst ? 'Hide this instance (Del)' : 'Hide this placement (Del)'" @click="m3d?.hideSelection('selection')">hide {{ selInst && card.model.instances > 1 ? 'instance' : '' }}</button>
                  <button class="rounded border border-line px-1.5 text-dim hover:text-fg" title="Hide every placement of this model (Shift+Del)" @click="m3d?.hideSelection('model')">hide model everywhere</button>
                </div>
                <div v-if="cardLook" class="flex flex-wrap items-center gap-x-2"><span class="w-16 shrink-0 text-dim">look</span><Guid :guid="cardLook.look" /><OpenIn :guid="cardLook.look" tab="model" /><span v-if="cardLook.triangles" class="text-faint">{{ cardLook.triangles.toLocaleString() }} △</span></div>
                <div v-if="cardLook?.slots?.length" class="mt-0.5 flex flex-wrap gap-1">
                  <span v-for="sl in cardLook.slots" :key="sl.slot" class="flex items-center gap-1 rounded bg-well px-1 py-0.5" :title="`slot ${sl.slot}, ${sl.submeshes} submeshes`">
                    <img v-if="sl.color" :src="`/thumb/${sl.color}.png`" class="h-5 w-5 rounded-sm object-cover" loading="lazy">
                    <Guid v-if="sl.material" :guid="sl.material" /><span v-else class="text-faint">slot {{ sl.slot }}: none</span>
                  </span>
                </div>
              </template>
              <div class="mt-1 flex items-center gap-2">
                <span v-if="card.pos" class="mono text-faint">({{ card.pos.join(', ') }}) yaw {{ card.yawDeg }}°</span>
                <OpenIn class="ml-auto" :ask="cardAsk" />
              </div>
            </template>
          </template>
        </div>
        <div class="absolute top-2 left-2 max-h-[70%] overflow-auto rounded bg-panel/90 px-2 py-1.5 text-[11px]" @mousedown.stop @mouseup.stop @wheel.stop>
          <button class="mb-1 text-dim hover:text-fg" @click="legend = !legend">{{ legend ? 'Hide layers' : 'Layers' }}</button>
          <template v-if="legend">
          <label v-for="(c, k) in CLS" :key="k" class="flex cursor-pointer items-center gap-2 py-px">
            <input v-model="shown[k]" type="checkbox" class="accent-[#8fb4ff]" />
            <span class="inline-block h-2 w-2 rounded-full" :style="{ background: c.c }" />
            <span :class="shown[k] ? '' : 'text-faint'">{{ c.label }}</span>
            <span class="mono text-faint">{{ v.classes[k] ?? 0 }}</span>
          </label>
          </template>
        </div>
      </div>
    </div>

    <Splitter id="map.side" :def="400" :min="260" :max="1000" side="after" />
    <aside class="flex min-h-0 shrink-0 flex-col" :style="{ width: sideW + 'px' }">
      <div class="flex gap-2 border-b border-line px-3 pt-2" role="tablist">
        <button v-for="t in (['placements', 'assets', 'info'] as const)" :key="t" class="tab" :aria-selected="side === t" @click="side = t">{{ t === 'placements' ? 'Placements' : t === 'assets' ? `Assets ${v.assets.length}` : 'Header' }}</button>
      </div>

      <template v-if="side === 'placements'">
        <input v-model="filter" class="mx-3 my-2 rounded border border-line bg-well px-2 py-1 text-[12px] outline-none focus:border-focus" placeholder="Filter: name, GUID, class, type, placed index (hex), override type" />
        <div class="min-h-0 overflow-auto text-[12px]" :class="detail ? 'shrink-0' : 'flex-1'" :style="detail ? { height: listH + 'px' } : {}">
          <button v-for="x in list.slice(0, LIMIT)" :key="x.i" class="flex w-full items-baseline gap-2 px-3 py-0.5 text-left hover:bg-raised" :class="sel === x.i ? 'bg-sel' : ''" @click="focus(x.i)">
            <span class="inline-block h-2 w-2 shrink-0 rounded-full" :style="{ background: cls(x.class).c }" />
            <span class="mono w-10 shrink-0 text-right text-faint">{{ x.placed >= 0 ? x.placed.toString(16) : '' }}</span>
            <span class="mono shrink-0"><Guid v-if="x.guid" :guid="x.guid" :link="false" /><span v-else class="text-dim">{{ TYPE_NAMES[x.type] }}</span></span>
            <span class="truncate text-dim">{{ x.name || (x.guid ? TYPE_NAMES[x.type] : '') }}</span>
          </button>
          <p v-if="list.length > LIMIT" class="px-3 py-1 text-faint">{{ num(list.length - LIMIT) }} more: narrow the filter</p>
        </div>
        <Splitter v-if="detail" id="map.list" :def="300" :min="80" :max="1200" vertical />
        <div v-if="detail" class="selectable min-h-0 flex-1 overflow-auto border-t border-line px-3 py-2 text-[12px]">
          <div v-if="detail.error" class="text-v-err">{{ detail.error }}</div>
          <template v-else>
            <p class="font-medium">{{ TYPE_NAMES[detail.type] }} <span class="text-faint">#{{ detail.i }}{{ detail.placed >= 0 ? ` · placed 0x${detail.placed.toString(16)}` : '' }} · {{ detail.size }} bytes at 0x{{ detail.offset.toString(16) }} · flags {{ detail.flags }}</span></p>
            <p class="mono text-faint">uuid {{ detail.uuid }}</p>
            <p v-if="detail.guid" class="mt-1"><Guid :guid="detail.guid" /> <span class="text-dim">{{ detail.name }}</span></p>
            <p v-if="detail.hasPos" class="mono mt-1 text-dim">pos ({{ fmt(detail.pos) }}) yaw {{ detail.yaw.toFixed(3) }}</p>
            <p v-if="detail.instances" class="text-dim">{{ detail.instances }} instances</p>
            <table class="mt-2 w-full">
              <tr v-for="(f, k) in detail.fields" :key="k" class="align-baseline">
                <td class="mono pr-2 text-faint">{{ f.at }}</td>
                <td class="pr-2 text-dim">{{ f.name }}</td>
                <td class="mono break-all"><template v-if="f.guid"><Guid :guid="f.guid" /> <span class="text-dim">{{ f.refName }}</span></template><template v-else>{{ f.value }}</template></td>
              </tr>
            </table>
            <div v-for="(b, k) in detail.instanceData" :key="k" class="mt-2">
              <p class="text-dim">instance data {{ k }}: slot {{ b.slot }}, {{ b.size }} bytes at {{ b.offset }} · <span class="text-fg">{{ b.types.join(', ') }}</span></p>
              <pre class="mono mt-1 max-h-72 overflow-auto rounded bg-well p-2 text-[11px] leading-snug">{{ b.json }}</pre>
            </div>
          </template>
        </div>
      </template>

      <div v-else-if="side === 'assets'" class="min-h-0 flex-1 overflow-auto px-3 py-2 text-[12px]">
        <p class="mb-2 text-dim">Resources the map data references directly (records and environment; model-group looks). Uses = records referencing it.</p>
        <div v-for="a in v.assets" :key="a.guid" class="flex items-baseline gap-2 py-0.5">
          <span class="mono w-8 shrink-0" :style="{ color: typeColor(a.type) }">{{ hex3(a.type) }}</span>
          <Guid :guid="a.guid" :missing="a.missing" />
          <span class="mono w-10 shrink-0 text-right text-faint">{{ a.count }}</span>
          <span class="truncate text-dim">{{ a.name }}</span>
        </div>
      </div>

      <div v-else class="selectable min-h-0 flex-1 overflow-auto px-3 py-2 text-[12px]">
        <h3 class="mb-1 font-medium">Environment</h3>
        <div v-for="e in v.env" :key="e.label" class="flex items-baseline gap-2 py-0.5">
          <span class="w-44 shrink-0 text-dim">{{ e.label }}</span><Guid :guid="e.guid" /><span class="truncate text-dim">{{ e.name }}</span>
        </div>
        <p class="mono mt-1 text-dim">sun direction ({{ fmt(v.sunDir) }}) · camera ({{ fmt(v.camera.slice(0, 3)) }})</p>
        <h3 class="mt-3 mb-1 font-medium">Record sections</h3>
        <table class="w-full">
          <tr v-for="s in v.sections" :key="s.type"><td class="mono pr-2 text-faint">{{ s.type }}</td><td class="pr-2">{{ s.name }}</td><td class="mono pr-2 text-right">{{ num(s.count) }}</td><td class="mono text-right text-dim">{{ bytes(s.size) }}</td><td class="mono pl-2 text-faint">{{ s.offset }}</td></tr>
        </table>
        <h3 class="mt-3 mb-1 font-medium">Bounds</h3>
        <p v-for="(b, k) in v.bounds" :key="k" class="mono text-dim">{{ k }}: ({{ fmt(b.min.slice(0, 3)) }}) – ({{ fmt(b.max.slice(0, 3)) }})</p>
        <p class="mt-3 text-dim">{{ v.soundLinks }} sound-volume → sound-space links · tactical data {{ bytes(v.tacticalSize) }}</p>
      </div>
    </aside>
  </div>
</template>
