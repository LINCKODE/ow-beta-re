<script setup lang="ts">
// Material preview (T071): the material on a sphere, plane or cube with three.js PBR (MeshPhysicalMaterial), a
// studio environment generated in code (RoomEnvironment), a rotatable key light, per-input toggles, a channel view
// and UV tiling. The inputs are resolved in Go (internal/model/preview.go) and served one image per input as
// /mprev/<material>/<input>.png (colour as stored in sRGB, normal with Z rebuilt, the scalar inputs grey).
// docs/Explorer.md "Material view".
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { MaterialBuilder, studioEnvironment } from '~/utils/owmat'
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ v: any }>()
const { s: ex } = useExplorer()
const pv = computed(() => p.v.preview)
const mode = computed<string>(() => pv.value.family.mode)
const inputs = computed<any[]>(() => pv.value.inputs)
const has = (i: string) => inputs.value.some((x: any) => x.input === i)

const geometry = ref<'sphere' | 'plane' | 'cube'>(pv.value.family.geometry)
const view = ref('lit')                      // 'lit' or an input name (channel view)
const on = reactive<Record<string, boolean>>({})
for (const i of inputs.value) on[i.input] = i.on // inputs on UV1+ start off (a primitive has one UV set)
const tiling = ref(1)
const hasUV = computed(() => pv.value.params.some((x: any) => x.use === 'uv scale' || x.use === 'uv offset'))
const useUV = ref(true)                      // apply the material's uv scale / offset (0x704029EA, 0x811D50FA) [D]
const lightAz = ref(40)
const lightEl = ref(35)
const envI = ref(1)
const bg = ref<'dark' | 'studio' | 'light' | 'checker'>(['effect', 'decal', 'alphatest'].includes(mode.value) ? 'checker' : 'dark')
const flipGreen = ref(false)
const texSize = ref(1024)
const loading = ref(0)
const opts = usePanel('material.options', 1) // the options panel: 1 open, 0 folded (remembered)
function toggleOpts() { opts.value = opts.value ? 0 : 1; opts.commit() }

const wrap = ref<HTMLDivElement>()
let renderer: THREE.WebGLRenderer | null = null
let scene: THREE.Scene, camera: THREE.PerspectiveCamera, controls: OrbitControls
let light: THREE.DirectionalLight, envTex: THREE.Texture, checkerTex: THREE.Texture
let root: THREE.Group
let raf = 0
let dirty = true
const builder = new MaterialBuilder(() => { loading.value = builder.pending; dirty = true })

// the lit material comes from the shared builder (utils/owmat.ts: also the Model tab's and the 3D map's PBR mode)
function makeMaterial(): THREE.Material {
  if (view.value !== 'lit') { // channel view: the input's image as stored
    const t = builder.tex(p.v.guid, view.value, true, texSize.value)
    t.repeat.set(tiling.value, tiling.value)
    t.offset.set(0, 0)
    loading.value = builder.pending
    return new THREE.MeshBasicMaterial({ map: t, side: THREE.DoubleSide })
  }
  const m = builder.build(p.v.guid, pv.value, { size: texSize.value, context: 'preview', physical: true, on: { ...on },
    tiling: tiling.value, useUV: useUV.value, flipGreen: flipGreen.value, envIntensity: envI.value })!
  loading.value = builder.pending
  return m
}

// geometries with v flipped: image row 0 at the top (D3D texture coordinates, like the Model tab)
function flipV(g: THREE.BufferGeometry) {
  const uv = g.getAttribute('uv') as THREE.BufferAttribute
  for (let i = 0; i < uv.count; i++) uv.setY(i, 1 - uv.getY(i))
  // the layer patch (utils/owmat.ts) reads vertex colour and UV1: white (0x25 shows its layer B) and UV1 = UV0
  g.setAttribute('owColor', new THREE.BufferAttribute(new Uint8Array(uv.count * 4).fill(255), 4, true))
  g.setAttribute('owUv1', uv)
  return g
}
function makeGeometry() {
  if (geometry.value === 'plane') return flipV(new THREE.PlaneGeometry(2, 2))
  if (geometry.value === 'cube') return flipV(new THREE.BoxGeometry(1.3, 1.3, 1.3))
  return flipV(new THREE.SphereGeometry(1, 128, 64))
}

function build() {
  if (!renderer) return
  root.children.forEach((c: any) => { c.geometry?.dispose(); c.material?.dispose() })
  root.clear()
  const g = makeGeometry()
  const mesh = new THREE.Mesh(g, makeMaterial())
  if (geometry.value === 'plane') mesh.rotation.x = -0.6
  root.add(mesh)
  if (view.value === 'lit' && mode.value === 'decal') { // the surface the decal lies on
    const base = new THREE.Mesh(makeGeometry(), new THREE.MeshStandardMaterial({ color: 0x8a8a8a, roughness: 0.7, side: THREE.DoubleSide }))
    base.rotation.copy(mesh.rotation)
    base.renderOrder = -1
    root.add(base)
    mesh.renderOrder = 1
  }
  dirty = true
}

function applyBackground() {
  if (!renderer) return
  scene.background = bg.value === 'studio' ? envTex : bg.value === 'checker' ? checkerTex
    : new THREE.Color(bg.value === 'light' ? 0xc9ced6 : 0x10151f)
  scene.environmentIntensity = envI.value
  dirty = true
}
function applyLight() {
  const az = lightAz.value * Math.PI / 180, el = lightEl.value * Math.PI / 180
  light?.position.set(Math.sin(az) * Math.cos(el) * 5, Math.sin(el) * 5, Math.cos(az) * Math.cos(el) * 5)
  dirty = true
}

function checker() {
  const c = document.createElement('canvas')
  c.width = c.height = 64
  const g = c.getContext('2d')!
  g.fillStyle = '#1b2230'; g.fillRect(0, 0, 64, 64)
  g.fillStyle = '#2a3446'; g.fillRect(0, 0, 32, 32); g.fillRect(32, 32, 32, 32)
  const t = new THREE.CanvasTexture(c)
  t.wrapS = t.wrapT = THREE.RepeatWrapping
  t.repeat.set(12, 8)
  t.colorSpace = THREE.SRGBColorSpace
  return t
}

function init() {
  const el = wrap.value!
  renderer = new THREE.WebGLRenderer({ antialias: true, preserveDrawingBuffer: false })
  renderer.setPixelRatio(window.devicePixelRatio)
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  el.appendChild(renderer.domElement)
  scene = new THREE.Scene()
  envTex = studioEnvironment(renderer)
  scene.environment = envTex
  checkerTex = checker()
  light = new THREE.DirectionalLight(0xffffff, 2.2)
  scene.add(light)
  camera = new THREE.PerspectiveCamera(35, 1, 0.01, 100)
  camera.position.set(0, 0.4, 3.6)
  root = new THREE.Group()
  scene.add(root)
  controls = new OrbitControls(camera, renderer.domElement)
  controls.addEventListener('change', () => { dirty = true; report() })
  controls.addEventListener('start', () => { moved = true })
  new ResizeObserver(resize).observe(el)
  resize()
  applyLight()
  applyBackground()
  build()
  const loop = () => { raf = requestAnimationFrame(loop); if (dirty && renderer) { dirty = false; renderer.render(scene, camera) } }
  loop()
}
function resize() {
  if (!renderer || !wrap.value) return
  const w = wrap.value.clientWidth, h = wrap.value.clientHeight
  renderer.setSize(w, h)
  camera.aspect = w / Math.max(1, h)
  camera.updateProjectionMatrix()
  if (!moved) fit()
  dirty = true
}
// fit: the unit sphere (radius 1.15 with margin) inside both the vertical and the horizontal field of view
let moved = false
function fit() {
  const v = camera.fov * Math.PI / 360
  const hz = Math.atan(Math.tan(v) * camera.aspect)
  const d = 1.2 / Math.sin(Math.min(v, hz))
  const dir = new THREE.Vector3(0, 0.12, 1).normalize()
  camera.position.copy(dir.multiplyScalar(d))
  controls?.target.set(0, 0, 0)
  controls?.update()
  dirty = true
}
function reset() { moved = false; fit() }

onMounted(init)
onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  root?.children.forEach((c: any) => { c.geometry?.dispose(); c.material?.dispose() })
  builder.dispose()
  envTex?.dispose()
  checkerTex?.dispose()
  renderer?.dispose()
  renderer?.domElement.remove()
  renderer = null
  delete ex.extra.material
})

watch([geometry, view, tiling, useUV, flipGreen, texSize, () => ({ ...on })], build, { deep: true })
watch([bg, envI], () => { applyBackground(); build() })
watch([lightAz, lightEl], applyLight)

// MCP current_view
function report() {
  const c = camera?.position
  ex.extra.material = {
    material: p.v.guid, family: pv.value.family.id, mode: mode.value, geometry: geometry.value, view: view.value,
    inputs: Object.fromEntries(inputs.value.map((i: any) => [i.input, !!on[i.input]])), tiling: tiling.value,
    materialUVScale: useUV.value, light: { azimuth: lightAz.value, elevation: lightEl.value }, background: bg.value,
    flipGreen: flipGreen.value, textureSize: texSize.value,
    camera: c ? [+c.x.toFixed(3), +c.y.toFixed(3), +c.z.toFixed(3)] : undefined,
  }
}
watch([geometry, view, tiling, useUV, lightAz, lightEl, bg, flipGreen, texSize, () => ({ ...on })], report, { immediate: true, deep: true })
const label: Record<string, string> = { color: 'colour', opacity: 'opacity', normal: 'normal', roughness: 'roughness', specular: 'specular', metalness: 'metal', ao: 'AO', emissive: 'emissive', colorB: 'layer B', height: 'blend height', overlay: 'overlay' }
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-1.5 text-[12px]">
      <div class="flex overflow-hidden rounded border border-line">
        <button v-for="g in (['sphere', 'plane', 'cube'] as const)" :key="g" class="px-2 py-0.5" :class="geometry === g ? 'bg-raised text-fg' : 'text-dim'" @click="geometry = g">{{ g }}</button>
      </div>
      <div class="flex overflow-hidden rounded border border-line" title="lit: the preview; an input: its image as stored (channel view)">
        <button class="px-2 py-0.5" :class="view === 'lit' ? 'bg-raised text-fg' : 'text-dim'" @click="view = 'lit'">lit</button>
        <button v-for="i in inputs" :key="i.input" class="px-2 py-0.5" :class="view === i.input ? 'bg-raised text-fg' : 'text-dim'" @click="view = i.input">{{ label[i.input] }}</button>
      </div>
      <span v-if="view === 'lit' && inputs.length" class="flex items-center gap-2">
        <label v-for="i in inputs" :key="i.input" class="flex items-center gap-1 text-dim" :title="`${i.slotName || i.slot} · ${i.channel}${i.uvSet ? ' · UV' + i.uvSet : ''} · [${i.conf}] ${i.note ?? ''}`" :class="i.uvSet ? 'italic' : ''"><input v-model="on[i.input]" type="checkbox">{{ label[i.input] }}</label>
      </span>
      <label v-if="has('normal')" class="flex items-center gap-1 text-dim" title="Read the normal map's green channel as up (OpenGL); default: down (Direct3D) [?]"><input v-model="flipGreen" type="checkbox">flip green</label>
      <button class="ml-auto text-dim hover:text-fg" @click="reset">Reset view</button>
      <button class="text-dim hover:text-fg" :class="opts ? 'text-fg' : ''" title="Light, environment, background, UV tiling, texture size" @click="toggleOpts">Options {{ opts ? '▴' : '▾' }}</button>
    </div>
    <div class="relative min-h-0 flex-1">
      <div ref="wrap" class="absolute inset-0" />
      <div class="pointer-events-none absolute bottom-2 left-3 text-[11.5px] text-dim">
        <span v-if="loading > 0">loading textures…</span>
        <span v-else>drag: rotate · right-drag: pan · wheel: zoom</span>
      </div>
      <div v-if="opts" class="absolute right-2 top-2 flex w-52 flex-col gap-1 rounded border border-line bg-panel/85 p-2 text-[11.5px] text-dim">
        <label class="flex items-center gap-2">tiling <input v-model.number="tiling" type="range" min="0.25" max="8" step="0.25" class="flex-1"><span class="mono w-8 text-right">{{ tiling }}</span></label>
        <label v-if="hasUV" class="flex items-center gap-1" :title="`parameters 704029ea (scale) / 811d50fa (offset): uv0 × scale + offset [D]`"><input v-model="useUV" type="checkbox">material uv ×({{ pv.uvScale.join(', ') }}) +({{ pv.uvOffset.join(', ') }})</label>
        <label class="flex items-center gap-2">light az <input v-model.number="lightAz" type="range" min="-180" max="180" step="5" class="flex-1"><span class="mono w-8 text-right">{{ lightAz }}</span></label>
        <label class="flex items-center gap-2">light el <input v-model.number="lightEl" type="range" min="-30" max="90" step="5" class="flex-1"><span class="mono w-8 text-right">{{ lightEl }}</span></label>
        <label class="flex items-center gap-2">env <input v-model.number="envI" type="range" min="0" max="2" step="0.1" class="flex-1"><span class="mono w-8 text-right">{{ envI }}</span></label>
        <div class="flex items-center gap-1">bg
          <button v-for="b in (['dark', 'studio', 'light', 'checker'] as const)" :key="b" class="rounded px-1" :class="bg === b ? 'bg-raised text-fg' : ''" @click="bg = b">{{ b }}</button>
        </div>
        <div class="flex items-center gap-1">tex
          <button v-for="t in [256, 512, 1024, 2048]" :key="t" class="rounded px-1" :class="texSize === t ? 'bg-raised text-fg' : ''" @click="texSize = t">{{ t }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
