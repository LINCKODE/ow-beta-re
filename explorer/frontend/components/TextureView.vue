<script setup lang="ts">
// Texture (0x004) or texture payload (0x04D → its texture): the decoded image of one mip / surface, channel and
// exposure controls, pixel inspector, header, mips and payloads, PNG export (docs/Explorer.md "Texture view").
// Decoding happens in Go (internal/texture); the image comes over the asset server (/tex/…), the canvas only
// isolates channels.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'
import { bytes, num } from '~/utils/format'

const p = defineProps<{ guid: string }>()
const { s: ex } = useExplorer()
const v = ref<any>(null)
const err = ref('')
const mip = ref(0)
const surface = ref(0)
const channel = ref<'rgba' | 'rgb' | 'r' | 'g' | 'b' | 'a'>('rgba')
const exposure = ref(0)
const auto = ref(true)
const zxy = ref(false)
const loading = ref(false)
const imgErr = ref('')
const note = ref('')
const sideW = usePanel('texture.side', 330)

const FACES = ['+X', '−X', '+Y', '−Y', '+Z', '−Z']
const surfaceLabel = (i: number) => (v.value?.cube ? `${Math.floor(i / 6) ? `cube ${Math.floor(i / 6)} ` : ''}${FACES[i % 6]}` : `${i}`)

onMounted(async () => {
  try {
    v.value = await api.Texture(p.guid)
    // A payload opens on its largest mip; a texture on the largest available one.
    const pl = v.value.payloads.find((x: any) => x.guid === v.value.payload)
    const best = v.value.mips.findIndex((m: any) => m.available)
    mip.value = pl ? pl.first : Math.max(0, best)
    zxy.value = v.value.channels === 2 && !v.value.hdr // BC5 / R8G8: normal maps (texture.DefaultDisplay)
    if (ex.instance >= 0 && ex.instance < v.value.mips.length) mip.value = ex.instance
    await nextTick()
    if (wrap.value) new ResizeObserver(() => draw()).observe(wrap.value)
    load()
  } catch (e: any) { err.value = String(e) }
})

// ---- image: <img> from /tex/… → source canvas (channel view) → drawn with zoom / pan ----
const wrap = ref<HTMLDivElement>()
const cv = ref<HTMLCanvasElement>()
let src: HTMLCanvasElement | null = null // the processed image at 1:1
let raw: ImageData | null = null          // the decoded 8-bit RGBA (inspector)
const view = ref({ zoom: 1, x: 0, y: 0 })
const hover = ref<null | { x: number; y: number; px: number[] }>(null)
let seq = 0

// Cube textures can be shown as a cross (faces +Y / −X +Z +X −Z / −Y) of the cube the selected face belongs to.
const cross = ref(false)
// MCP current_view: what is displayed; an MCP select (mip) arrives as the pane's instance
ex.extra.texture = {}
watch([mip, surface, channel, exposure, auto, zxy, cross, () => view.value.zoom, hover], () => {
  ex.extra.texture = { mip: mip.value, surface: surface.value, channels: channel.value, exposure: exposure.value, autoExposure: auto.value,
    zFromXY: zxy.value, cubeCross: cross.value, zoom: +view.value.zoom.toFixed(3),
    hoverPixel: hover.value ? { x: hover.value.x, y: hover.value.y, rgba: hover.value.px } : undefined }
}, { immediate: true })
watch(() => ex.instance, (i) => { if (v.value && i >= 0 && i < v.value.mips.length) mip.value = i })
onBeforeUnmount(() => { delete ex.extra.texture; if (ex.extra.ask?.tab === 'texture') delete ex.extra.ask })
// the Ask panel follows the texture and its mip (useAsk liveSubject)
watch([v, mip], () => {
  if (v.value?.guid) ex.extra.ask = { tab: 'texture', subject: { kind: 'texture', guid: v.value.guid, mip: mip.value, label: `texture ${v.value.guid} mip ${mip.value}${v.value.mips?.[mip.value] ? ` (${v.value.mips[mip.value].width}×${v.value.mips[mip.value].height})` : ''}` } }
}, { immediate: true })
const CROSS: [number, number][] = [[2, 1], [0, 1], [1, 0], [1, 2], [1, 1], [3, 1]] // face → (column, row), D3D order
const surfaces = computed(() => {
  if (!v.value) return []
  if (!(cross.value && v.value.cube)) return [surface.value]
  const c0 = Math.floor(surface.value / 6) * 6
  return [0, 1, 2, 3, 4, 5].map((k) => c0 + k)
})
const url = computed(() => {
  if (!v.value) return ''
  const q = `exp=${exposure.value}&auto=${auto.value ? 1 : 0}&z=${zxy.value ? 1 : 0}`
  return surfaces.value.map((sf) => `/tex/${v.value.guid}/${mip.value}/${sf}.png?${q}`).join(' ')
})
const image = (u: string) => new Promise<HTMLImageElement>((ok, bad) => {
  const im = new Image()
  im.onload = () => ok(im)
  im.onerror = bad
  im.src = u
})

async function load() {
  if (!v.value?.decoder || !v.value.mips[mip.value]?.available) { src = null; raw = null; draw(); return }
  const n = ++seq
  loading.value = true
  imgErr.value = ''
  let ims: HTMLImageElement[]
  try {
    ims = await Promise.all(url.value.split(' ').map(image))
  } catch {
    if (n === seq) { loading.value = false; imgErr.value = 'The image did not decode (see Info).' }
    return
  }
  if (n !== seq) return
  loading.value = false
  const w = ims[0].naturalWidth, h = ims[0].naturalHeight
  const c = document.createElement('canvas')
  c.width = ims.length > 1 ? w * 4 : w
  c.height = ims.length > 1 ? h * 3 : h
  const g = c.getContext('2d', { willReadFrequently: true })!
  ims.forEach((im, k) => {
    const [x, y] = ims.length > 1 ? CROSS[k] : [0, 0]
    g.drawImage(im, x * w, y * h)
  })
  raw = g.getImageData(0, 0, c.width, c.height)
  const first = !src || src.width !== c.width || src.height !== c.height
  src = c
  applyChannel()
  if (first) fit()
  else draw()
}

function applyChannel() {
  if (!src || !raw) return
  const g = src.getContext('2d')!
  const d = new ImageData(new Uint8ClampedArray(raw.data), raw.width, raw.height)
  const a = d.data
  const ch = channel.value
  if (ch !== 'rgba') {
    const k = { r: 0, g: 1, b: 2, a: 3 }[ch as 'r']
    for (let i = 0; i < a.length; i += 4) {
      if (ch === 'rgb') { a[i + 3] = 255; continue }
      const x = a[i + k]
      a[i] = a[i + 1] = a[i + 2] = x
      a[i + 3] = 255
    }
  }
  g.putImageData(d, 0, 0)
  draw()
}

function fit() {
  const w = wrap.value
  if (!w || !src) return
  const z = Math.min((w.clientWidth - 24) / src.width, (w.clientHeight - 24) / src.height)
  const zoom = z >= 1 ? Math.max(1, Math.floor(z)) : z
  view.value = { zoom, x: (w.clientWidth - src.width * zoom) / 2, y: (w.clientHeight - src.height * zoom) / 2 }
  draw()
}
function actual() {
  const w = wrap.value
  if (!w || !src) return
  view.value = { zoom: 1, x: Math.round((w.clientWidth - src.width) / 2), y: Math.round((w.clientHeight - src.height) / 2) }
  draw()
}

function draw() {
  const c = cv.value, w = wrap.value
  if (!c || !w) return
  const dpr = window.devicePixelRatio || 1
  c.width = w.clientWidth * dpr
  c.height = w.clientHeight * dpr
  c.style.width = w.clientWidth + 'px'
  c.style.height = w.clientHeight + 'px'
  const g = c.getContext('2d')!
  g.setTransform(dpr, 0, 0, dpr, 0, 0)
  g.clearRect(0, 0, w.clientWidth, w.clientHeight)
  if (!src) return
  const { zoom, x, y } = view.value
  const W = src.width * zoom, H = src.height * zoom
  // Checkerboard under the image (alpha).
  g.save()
  g.beginPath()
  g.rect(x, y, W, H)
  g.clip()
  const cs = 8
  for (let yy = Math.floor(Math.max(0, -y) / cs) * cs; yy < Math.min(H, w.clientHeight - y); yy += cs) {
    for (let xx = Math.floor(Math.max(0, -x) / cs) * cs; xx < Math.min(W, w.clientWidth - x); xx += cs) {
      g.fillStyle = ((xx + yy) / cs) % 2 ? '#2a3348' : '#1f2738'
      g.fillRect(x + xx, y + yy, cs, cs)
    }
  }
  g.imageSmoothingEnabled = zoom < 1
  g.drawImage(src, x, y, W, H)
  g.restore()
  g.strokeStyle = '#34425f'
  g.lineWidth = 1
  g.strokeRect(x - 0.5, y - 0.5, W + 1, H + 1)
  // Pixel grid at high zoom.
  if (zoom >= 12) {
    g.strokeStyle = 'rgba(0,0,0,0.25)'
    g.beginPath()
    for (let i = 1; i < src.width; i++) { g.moveTo(x + i * zoom + 0.5, y); g.lineTo(x + i * zoom + 0.5, y + H) }
    for (let i = 1; i < src.height; i++) { g.moveTo(x, y + i * zoom + 0.5); g.lineTo(x + W, y + i * zoom + 0.5) }
    g.stroke()
  }
}

function wheel(e: WheelEvent) {
  const w = wrap.value
  if (!w || !src) return
  const r = w.getBoundingClientRect()
  const mx = e.clientX - r.left, my = e.clientY - r.top
  const { zoom, x, y } = view.value
  const f = e.deltaY < 0 ? 1.25 : 0.8
  const nz = Math.min(64, Math.max(1 / 64, zoom * f))
  view.value = { zoom: nz, x: mx - ((mx - x) / zoom) * nz, y: my - ((my - y) / zoom) * nz }
  draw()
}
let drag: null | { x: number; y: number; vx: number; vy: number } = null
function down(e: MouseEvent) {
  if (e.button !== 0) return
  drag = { x: e.clientX, y: e.clientY, vx: view.value.x, vy: view.value.y }
}
function move(e: MouseEvent) {
  const w = wrap.value
  if (!w) return
  if (drag) {
    view.value = { ...view.value, x: drag.vx + e.clientX - drag.x, y: drag.vy + e.clientY - drag.y }
    draw()
  }
  if (!raw) { hover.value = null; return }
  const r = w.getBoundingClientRect()
  const ix = Math.floor((e.clientX - r.left - view.value.x) / view.value.zoom)
  const iy = Math.floor((e.clientY - r.top - view.value.y) / view.value.zoom)
  if (ix < 0 || iy < 0 || ix >= raw.width || iy >= raw.height) { hover.value = null; return }
  const o = (iy * raw.width + ix) * 4
  hover.value = { x: ix, y: iy, px: Array.from(raw.data.slice(o, o + 4)) }
}
function up() { drag = null }
function leave() { drag = null; hover.value = null }

watch(url, load)
watch(channel, applyChannel)
watch(mip, (m) => { if (v.value && ex.instance !== m) ex.instance = m })
function key(e: KeyboardEvent) {
  if ((e.target as HTMLElement)?.tagName === 'INPUT' || !v.value) return
  if (e.key === '[' && mip.value > 0) mip.value--
  else if (e.key === ']' && mip.value < v.value.mips.length - 1) mip.value++
  else if (e.key === 'f') fit()
  else if (e.key === '1') actual()
}
onMounted(() => window.addEventListener('keydown', key))
onBeforeUnmount(() => window.removeEventListener('keydown', key))

const display = computed(() => ({ exposure: exposure.value, auto: auto.value, zFromXY: zxy.value }))
async function exportPNG() {
  note.value = ''
  try {
    const path = await api.ExportTexturePNG(v.value.guid, mip.value, surface.value, display.value)
    if (path) note.value = `Saved ${path}`
  } catch (e: any) { note.value = `Export failed: ${e}` }
}
async function exportAll() {
  note.value = ''
  try {
    const r = await api.ExportTextureAll(v.value.guid, display.value)
    if (r) note.value = `Saved ${r}`
  } catch (e: any) { note.value = `Export failed: ${e}` }
}
const m = computed(() => v.value?.mips[mip.value])
const hexHeader = computed(() => (v.value?.header.bytes ?? '').match(/.{1,2}/g)?.join(' ') ?? '')
const inPayload = (i: number) => v.value?.payload && v.value.mips[i]?.source === v.value.payload
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else-if="!v" class="p-6 text-dim">Loading…</div>
  <div v-else class="flex h-full min-h-0 min-w-0">
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-1.5 text-[12px]">
        <label class="flex items-center gap-1.5 text-dim">Mip
          <select v-model="mip" class="sel mono rounded border border-line bg-well px-1.5 py-0.5 text-fg outline-none focus:border-focus">
            <option v-for="(x, i) in v.mips" :key="i" :value="i" :disabled="!x.available">{{ i }} · {{ x.width }}×{{ x.height }}{{ x.available ? '' : ' (missing)' }}</option>
          </select>
        </label>
        <label v-if="v.surfaces > 1" class="flex items-center gap-1.5 text-dim">{{ v.cube ? 'Face' : 'Surface' }}
          <select v-model="surface" class="sel mono rounded border border-line bg-well px-1.5 py-0.5 text-fg outline-none focus:border-focus">
            <option v-for="i in v.surfaces" :key="i" :value="i - 1">{{ surfaceLabel(i - 1) }}</option>
          </select>
          <span class="text-faint">of {{ v.surfaces }}</span>
        </label>
        <label v-if="v.cube" class="flex items-center gap-1 text-dim" title="Show the six faces of this cube unfolded"><input v-model="cross" type="checkbox" class="accent-[#8fb4ff]" /> cross</label>
        <div class="flex overflow-hidden rounded border border-line" role="group" aria-label="Channels">
          <button v-for="c in (['rgba', 'rgb', 'r', 'g', 'b', 'a'] as const)" :key="c" class="mono px-1.5 py-0.5 uppercase hover:bg-raised"
            :class="channel === c ? 'bg-sel text-fg' : 'text-dim'" :title="c === 'rgba' ? 'Colour over a checkerboard (alpha)' : c === 'rgb' ? 'Colour, alpha ignored' : `The ${c.toUpperCase()} channel as grey`"
            @click="channel = c">{{ c }}</button>
        </div>
        <template v-if="v.hdr">
          <label class="flex items-center gap-1.5 text-dim" title="HDR: exposure in stops, then clamped and sRGB-encoded">Exposure
            <input v-model.number="exposure" type="range" min="-8" max="8" step="0.5" class="w-28 accent-[#8fb4ff]" />
            <span class="mono w-8 text-fg">{{ exposure > 0 ? '+' : '' }}{{ exposure }}</span>
          </label>
          <label class="flex items-center gap-1 text-dim" title="Scale so the 99.5th percentile maps to white, then apply the exposure"><input v-model="auto" type="checkbox" class="accent-[#8fb4ff]" /> auto</label>
        </template>
        <label v-if="v.channels === 2" class="flex items-center gap-1 text-dim" title="Two-channel data read as a normal: blue = sqrt(1 − x² − y²)"><input v-model="zxy" type="checkbox" class="accent-[#8fb4ff]" /> Z from XY</label>
        <span class="ml-auto flex items-center gap-1">
          <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Fit (F)" @click="fit">Fit</button>
          <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Actual size (1)" @click="actual">1:1</button>
          <OpenIn :ask="{ kind: 'texture', guid: v.guid, mip, label: `texture ${v.guid} mip ${mip} (${m?.width}×${m?.height})` }" />
          <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Save this mip / surface as PNG, as displayed" @click="exportPNG">Export PNG</button>
          <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Save every mip of every surface as PNG into a directory" @click="exportAll">Export all</button>
        </span>
      </div>
      <p v-if="v.payload" class="border-b border-line bg-raised/40 px-3 py-1 text-[12px] text-dim">
        Payload <Guid :guid="v.payload" :link="false" /> holds the highlighted mips of texture <Guid :guid="v.guid" />.
      </p>
      <p v-if="note" class="border-b border-line px-3 py-1 text-[12px] text-dim">{{ note }}</p>
      <div ref="wrap" class="relative min-h-0 flex-1 cursor-crosshair overflow-hidden bg-well" @wheel.prevent="wheel" @mousedown="down" @mousemove="move" @mouseup="up" @mouseleave="leave">
        <canvas ref="cv" class="absolute inset-0" />
        <p v-if="!v.decoder" class="absolute inset-0 flex items-center justify-center text-v-err">No decoder for {{ v.format }}.</p>
        <p v-else-if="v.error || imgErr" class="absolute inset-0 flex items-center justify-center text-v-err">{{ v.error || imgErr }}</p>
        <p v-else-if="m && !m.available" class="absolute inset-0 flex items-center justify-center text-dim">Mip {{ mip }} is not in the install.</p>
        <span v-if="loading" class="absolute top-2 right-3 text-[11px] text-faint">decoding…</span>
        <p class="pointer-events-none absolute right-3 bottom-2 rounded bg-panel/90 px-2 py-0.5 text-[11px] text-faint">
          <template v-if="hover">
            <span class="mono text-fg">{{ hover.x }}, {{ hover.y }}</span> ·
            <span class="mono">R {{ hover.px[0] }} G {{ hover.px[1] }} B {{ hover.px[2] }} A {{ hover.px[3] }}</span>
            <span class="ml-1 inline-block h-2.5 w-2.5 rounded-sm align-middle" :style="{ background: `rgb(${hover.px[0]},${hover.px[1]},${hover.px[2]})` }" /> ·
          </template>
          {{ Math.round(view.zoom * 100) }} % · drag to pan, scroll to zoom, [ ] mip, F fit, 1 actual size
        </p>
      </div>
    </div>

    <Splitter id="texture.side" :def="330" :min="240" :max="800" side="after" />
    <aside class="selectable min-h-0 shrink-0 overflow-auto px-4 py-3 text-[12px]" :style="{ width: sideW + 'px' }">
      <h3 class="mb-1 font-medium">Header</h3>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
        <dt class="text-dim">Format</dt><dd><span class="mono">{{ v.format }}</span> <span class="text-faint">(DXGI {{ v.dxgi }}{{ v.hdr ? ', HDR' : '' }})</span></dd>
        <dt class="text-dim">Size</dt><dd class="mono">{{ v.width }} × {{ v.height }}</dd>
        <dt class="text-dim">Mips</dt><dd class="mono">{{ v.mips.length }}</dd>
        <dt class="text-dim">Surfaces</dt><dd class="mono">{{ v.surfaces }}<span v-if="v.cube" class="text-faint"> (cube{{ v.surfaces > 6 ? ` array ×${v.surfaces / 6}` : '' }})</span></dd>
        <dt class="text-dim">Flags</dt><dd><span class="mono">0x{{ v.flags.toString(16).padStart(4, '0') }}</span> <span class="text-dim">{{ (v.flagNames ?? []).join(', ') }}</span></dd>
        <dt class="text-dim">Data</dt><dd>{{ v.inline ? `inline, ${bytes(v.dataSize)}` : `${v.payloads.length} payload${v.payloads.length === 1 ? '' : 's'}` }}</dd>
        <template v-if="v.base"><dt class="text-dim">Delta of</dt><dd><Guid :guid="v.base" /> <span class="text-faint">(colour blocks patched)</span></dd></template>
        <dt class="text-dim">+0x05</dt><dd class="mono">{{ v.unk5 }}</dd>
        <dt class="text-dim">+0x10</dt><dd class="mono">{{ v.unk10 }}</dd>
        <template v-if="v.variants.length"><dt class="text-dim">Variants</dt><dd class="flex flex-col"><Guid v-for="x in v.variants" :key="x" :guid="x" /></dd></template>
      </dl>
      <p class="mono mt-1.5 break-all text-[11px] text-faint">{{ hexHeader }}</p>

      <h3 class="mt-4 mb-1 font-medium">Mips</h3>
      <table class="w-full">
        <tbody>
          <tr v-for="(x, i) in v.mips" :key="i" class="cursor-pointer hover:bg-raised" :class="[i === mip ? 'bg-sel' : '', inPayload(i) ? 'text-focus' : '']" @click="x.available && (mip = i)">
            <td class="mono pr-2 text-faint">{{ i }}</td>
            <td class="mono pr-2">{{ x.width }}×{{ x.height }}</td>
            <td class="mono pr-2 text-right text-dim">{{ bytes(x.bytes * v.surfaces) }}</td>
            <td class="text-right"><Guid v-if="x.source" :guid="x.source" /><span v-else-if="x.available" class="text-faint">inline</span><span v-else class="text-v-err">missing</span></td>
          </tr>
        </tbody>
      </table>

      <template v-if="v.payloads.length">
        <h3 class="mt-4 mb-1 font-medium">Payloads</h3>
        <div v-for="x in v.payloads" :key="x.guid" class="mb-1.5">
          <Guid :guid="x.guid" />
          <span v-if="!x.present" class="ml-1 text-v-err">not in the install</span>
          <span v-else class="ml-1 text-dim">mips {{ x.first }}{{ x.count > 1 ? `–${x.first + x.count - 1}` : '' }} · {{ bytes(x.size) }}</span>
          <p v-if="x.error" class="text-v-err">{{ x.error }}</p>
        </div>
      </template>
      <p class="mt-4 text-[11px] leading-snug text-faint">Format: docs/AssetFormats.md §1. The image is decoded in Go and sent as PNG; HDR values are scaled by the exposure, clamped and sRGB-encoded; the inspector shows those 8-bit values.</p>
    </aside>
  </div>
</template>

<style scoped>
/* webkit2gtk draws native selects with the GTK theme (light): draw our own. */
.sel { appearance: none; -webkit-appearance: none; color-scheme: dark; padding-right: 18px;
  background-image: linear-gradient(45deg, transparent 50%, #8a98b0 50%), linear-gradient(135deg, #8a98b0 50%, transparent 50%);
  background-position: calc(100% - 9px) 50%, calc(100% - 5px) 50%; background-size: 4px 4px; background-repeat: no-repeat; }
.sel option { background: #111827; color: #d8e0ec; }
</style>
