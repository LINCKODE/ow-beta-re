<script setup lang="ts">
// One WEM: waveform (min / max per pixel column, decoded in Go: App.AudioPeaks), play / pause, seek by clicking the
// waveform, loop, volume, export. The sound is decoded in Go and fetched as 16-bit PCM (/audio/<guid>.pcm); WebAudio
// plays it from an AudioBuffer, so the webview decodes nothing (WebKitGTK's <audio> can't load from the asset
// server's scheme). Only one player sounds at a time (docs/Explorer.md "Audio view").
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'

const p = defineProps<{ guid: string; autoplay?: boolean }>()
const emit = defineEmits<{ ended: [] }>()
const { s: ex, flash } = useExplorer()
const cv = ref<HTMLCanvasElement | null>(null)
const box = ref<HTMLDivElement | null>(null)
const peaks = ref<any>(null)
const err = ref('')
const playing = ref(false)
const t = ref(0)
const loop = ref(false)
const vol = ref(savedVolume())
let raf = 0
let ro: ResizeObserver | null = null

// One AudioContext for the app; the current source is shared so starting one player stops the other.
const shared: { ctx: AudioContext | null; stop: (() => void) | null } =
  (window as any).__owAudio ?? ((window as any).__owAudio = { ctx: null, stop: null })
let buffer: AudioBuffer | null = null
let src: AudioBufferSourceNode | null = null
let gain: GainNode | null = null
let startedAt = 0 // ctx time at which position 0 would have played
let offset = 0 // position when paused

function savedVolume() {
  try { const v = parseFloat(localStorage.getItem('audio.volume') ?? ''); return isNaN(v) ? 0.8 : v } catch { return 0.8 }
}
watch(vol, v => {
  if (gain) gain.gain.value = v
  try { localStorage.setItem('audio.volume', String(v)) } catch {}
})

const dur = computed(() => peaks.value?.seconds ?? 0)
const fmt = (s: number) => {
  const m = Math.floor(s / 60)
  return `${m}:${(s - m * 60).toFixed(2).padStart(5, '0')}`
}
const db = (x: number) => (x > 0 ? (20 * Math.log10(x)).toFixed(1) + ' dB' : '−∞')

async function load() {
  err.value = ''
  peaks.value = null
  buffer = null
  const w = Math.max(64, Math.round((box.value?.clientWidth ?? 600) * (window.devicePixelRatio || 1)))
  try {
    peaks.value = await api.AudioPeaks(p.guid, w)
  } catch (e: any) { err.value = String(e); return }
  await nextTick()
  draw()
  if (p.autoplay) play()
}

// samples fetches the decoded PCM into an AudioBuffer (once).
async function samples(ctx: AudioContext): Promise<AudioBuffer> {
  if (buffer) return buffer
  const r = await fetch(`/audio/${p.guid}.pcm`)
  if (!r.ok) throw new Error(await r.text())
  const b = await r.arrayBuffer()
  const dv = new DataView(b)
  if (dv.getUint32(0, true) !== 0x4350574f) throw new Error('not OWPC data') // "OWPC"
  const chs = dv.getUint32(4, true), rate = dv.getUint32(8, true), n = dv.getUint32(12, true)
  const ab = ctx.createBuffer(chs, Math.max(1, n), rate)
  const s16 = new Int16Array(b, 16, chs * n)
  for (let c = 0; c < chs; c++) {
    const out = ab.getChannelData(c)
    for (let i = 0; i < n; i++) out[i] = s16[c * n + i] / 32767
  }
  buffer = ab
  return ab
}

function css(name: string) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() }

function draw() {
  const c = cv.value, pk = peaks.value
  if (!c || !pk || !box.value) return
  const dpr = window.devicePixelRatio || 1
  const W = box.value.clientWidth, H = box.value.clientHeight
  c.width = Math.round(W * dpr); c.height = Math.round(H * dpr)
  c.style.width = W + 'px'; c.style.height = H + 'px'
  const g = c.getContext('2d')!
  g.clearRect(0, 0, c.width, c.height)
  const chs = pk.channels.length
  const lane = c.height / chs
  const head = dur.value ? t.value / dur.value : 0
  const wave = css('--color-v-num') || '#a9c3ff', done = css('--color-focus') || '#8fb4ff', mid = css('--color-line') || '#253049'
  for (let ch = 0; ch < chs; ch++) {
    const a: number[] = pk.channels[ch]
    const n = a.length / 2
    const y0 = lane * ch + lane / 2, amp = lane / 2 - 2
    g.fillStyle = mid
    g.fillRect(0, Math.round(y0), c.width, 1)
    for (let x = 0; x < c.width; x++) {
      const i = Math.min(n - 1, Math.floor(x / c.width * n))
      const lo = a[2 * i], hi = a[2 * i + 1]
      const past = head > 0 && x / c.width <= head
      g.fillStyle = past ? done : wave
      g.globalAlpha = past ? 1 : 0.75
      const top = y0 - hi * amp, bot = y0 - lo * amp
      g.fillRect(x, top, 1, Math.max(1, bot - top))
    }
    g.globalAlpha = 1
  }
  if (head > 0) {
    g.fillStyle = css('--color-fg') || '#fff'
    g.fillRect(Math.round(head * c.width), 0, Math.max(1, dpr), c.height)
  }
}

function position(): number {
  if (!playing.value || !shared.ctx) return offset
  const d = dur.value || 1
  const pos = shared.ctx.currentTime - startedAt
  return loop.value ? pos % d : Math.min(pos, d)
}

function tick() {
  t.value = position()
  // The MCP snapshot is debounced (120 ms): a per-frame change would starve it, so report in 0.5 s steps.
  const a = ex.extra.audio
  if (a && (Math.abs((a.position ?? 0) - t.value) >= 0.5 || !playing.value)) a.position = +t.value.toFixed(3)
  draw()
  if (playing.value) raf = requestAnimationFrame(tick)
}

function stopSource() {
  if (src) {
    src.onended = null
    try { src.stop() } catch {}
    src.disconnect()
    src = null
  }
}

async function play(at?: number) {
  try {
    if (!shared.ctx) shared.ctx = new AudioContext()
    const ctx = shared.ctx
    if (ctx.state === 'suspended') await ctx.resume()
    const ab = await samples(ctx)
    if (shared.stop) shared.stop()
    stopSource()
    if (at !== undefined) offset = at
    if (offset >= ab.duration) offset = 0
    gain = ctx.createGain()
    gain.gain.value = vol.value
    gain.connect(ctx.destination)
    src = ctx.createBufferSource()
    src.buffer = ab
    src.loop = loop.value
    src.connect(gain)
    src.onended = () => { playing.value = false; offset = 0; t.value = 0; draw(); emit('ended') }
    src.start(0, offset)
    startedAt = ctx.currentTime - offset
    shared.stop = pause
    playing.value = true
    if (ex.extra.audio) ex.extra.audio.playing = true
    tick()
  } catch (e: any) { err.value = `Can't play: ${e}`; flash(`Can't play: ${e}`) }
}
function pause() {
  if (!playing.value) return
  offset = position()
  playing.value = false
  stopSource()
  if (shared.stop === pause) shared.stop = null
  if (ex.extra.audio) ex.extra.audio.playing = false
  tick()
}
function toggle() { playing.value ? pause() : play() }
function seek(ev: MouseEvent) {
  if (!box.value || !dur.value) return
  const r = box.value.getBoundingClientRect()
  const at = Math.max(0, Math.min(1, (ev.clientX - r.left) / r.width)) * dur.value
  if (playing.value) play(at)
  else { offset = at; t.value = at; draw() }
}
watch(loop, l => { if (src) src.loop = l })
function onKey(e: KeyboardEvent) {
  if (e.key === ' ' || e.key === 'k') { e.preventDefault(); e.stopPropagation(); toggle() }
  if (e.key === 'l') loop.value = !loop.value
}
async function exportAs(fmtName: 'wav' | 'ogg') {
  try {
    const path = await api.ExportAudio(p.guid, fmtName)
    if (path) flash(`Saved ${path}`)
  } catch (e: any) { flash(`Export failed: ${e}`) }
}

watch(() => p.guid, () => { pause(); offset = 0; t.value = 0; load() })
onMounted(() => {
  load()
  if (box.value) { ro = new ResizeObserver(() => draw()); ro.observe(box.value) }
})
onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  ro?.disconnect()
  pause()
})
defineExpose({ play, pause, toggle })
</script>

<template>
  <div class="flex flex-col gap-2 outline-none" tabindex="0" @keydown="onKey">
    <div ref="box" class="relative h-36 cursor-pointer overflow-hidden rounded border border-line bg-well" @click="seek">
      <canvas ref="cv" class="absolute inset-0" />
      <p v-if="!peaks && !err" class="absolute inset-0 grid place-items-center text-dim">decoding…</p>
      <p v-if="err" class="absolute inset-0 grid place-items-center px-4 text-center text-v-err">{{ err }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2 text-[12.5px]">
      <button class="w-24 whitespace-nowrap rounded bg-raised px-3 py-1 hover:bg-sel" :disabled="!peaks" :title="'Play / pause (Space)'" @click="toggle">
        {{ playing ? '❚❚ Pause' : '▶ Play' }}
      </button>
      <span class="mono w-36 text-dim">{{ fmt(t) }} / {{ fmt(dur) }}</span>
      <label class="flex items-center gap-1 text-dim" title="Loop (L)"><input v-model="loop" type="checkbox"> loop</label>
      <label class="flex items-center gap-1 text-dim">vol <input v-model.number="vol" type="range" min="0" max="1" step="0.01" class="w-24"></label>
      <span v-if="peaks" class="text-faint">peak {{ db(peaks.peak) }} · RMS {{ db(peaks.rms) }}</span>
      <span class="flex-1" />
      <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Decoded, 16-bit PCM" @click="exportAs('wav')">Export WAV</button>
      <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Rebuilt Ogg Vorbis (no re-encoding)" @click="exportAs('ogg')">Export OGG</button>
    </div>
  </div>
</template>
