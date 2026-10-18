<script setup lang="ts">
// 3D map mode (T066): the map's static collision (docs/Maps.md §2.6) with flat colours and simple shading, the
// placements as markers (same classes, layers, selection and list as the 2D plot), orbit and fly cameras, the origin
// gizmo, and the world position under the cursor (ray cast on the mesh's own BVH in Go: App.MapRay).
// World frame: the game's (x, y up, z); the 2D plot's view is the orbit camera looking down with x → right, z → down.
import * as THREE from 'three'
import { mergeGeometries } from 'three/examples/jsm/utils/BufferGeometryUtils.js'
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { api } from '~/composables/api'
import { LogPrint } from '~/wailsjs/runtime/runtime'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'
import { num } from '~/utils/format'
import { fetchMesh, materialTexture } from '~/utils/owmd'
import { MaterialBuilder, patchLayers, studioEnvironment, type Preview } from '~/utils/owmat'
import { disposeMapEnvironment, loadMapEnvironment, type MapEnv } from '~/utils/owenv'
import { loadProbes, patchProbes, type Probes } from '~/utils/owprobes'
import { EffectComposer } from 'three/examples/jsm/postprocessing/EffectComposer.js'
import { RenderPass } from 'three/examples/jsm/postprocessing/RenderPass.js'
import { OutputPass } from 'three/examples/jsm/postprocessing/OutputPass.js'
import { LUTPass } from 'three/examples/jsm/postprocessing/LUTPass.js'

const p = defineProps<{
  guid: string
  v: any // mapview.View
  shown: Record<string, boolean>
  cls: (k: string) => { c: string; r: number; label: string }
  sel: number
  selInst?: any // the picked model instance of the selected record (T064): { record, instance, model, look, pos }
  mirror: boolean // mirror world x (the 2D flips: H xor V changes handedness; both = a rotation)
  typeNames: string[]
}>()
const emit = defineEmits<{ select: [i: number, inst?: any] }>() // inst: the picked model instance (T064)
const { s: ex, pane, copy } = useExplorer()

const wrap = ref<HTMLDivElement>()
const cv = ref<HTMLCanvasElement>()
const gizmo = ref<HTMLCanvasElement>()
const status = ref('Loading the collision mesh…')
const err = ref('')
const info = reactive({ tris: 0, verts: 0, solid: 0, clip: 0, other: 0, hulls: 0, capsules: 0, spheres: 0, ms: 0 })

// ---- settings (remembered with the panel sizes: state.json) ----
const colorMode = usePanel('map3d.color', 0) // 0 surface, 1 height, 2 mask, 3 material
const COLOR_MODES = ['surface', 'height', 'mask', 'material']
const camMode = usePanel('map3d.fly', 1) // 0 orbit, 1 fly (default), 2 FPS (noclip, pointer lock)
const layers = reactive({
  solid: true, other: true, hulls: true, capsules: true, spheres: true, labels: true, wire: false, ghosts: true, clip: true,
})
const LAYER_KEY = 'map3d.layers'
const layerBits = usePanel(LAYER_KEY, 0b110111111) // bit per layers key, in order (all but wireframe)
const panelOpen = usePanel('map3d.panel', 1)
function togglePanel() { panelOpen.value = panelOpen.value ? 0 : 1; panelOpen.commit() }
const LAYER_NAMES = Object.keys(layers) as (keyof typeof layers)[]
LAYER_NAMES.forEach((k, i) => { layers[k] = !!(layerBits.value & (1 << i)) })
watch(() => ({ ...layers }), (l) => {
  let b = 0
  LAYER_NAMES.forEach((k, i) => { if (l[k]) b |= 1 << i })
  if (b !== layerBits.value) { layerBits.value = b; layerBits.commit() }
})

// ---- three.js scene ----
let renderer: THREE.WebGLRenderer | null = null
const scene = new THREE.Scene()
scene.background = new THREE.Color('#0e131e')
const world = new THREE.Group() // everything in game coordinates; scale.x = -1 when mirrored
scene.add(world)
// the geometry shown (T064): bit 0 collision (mesh + shapes), bit 1 the placed models, bit 2 helper / editor
// geometry (models' submeshes whose material has +0x68 bit 1: "Player Only / No Walk" boxes, placeholders); default
// models only
const geoBits = usePanel('map3d.geo', 2)
const showCol = computed(() => !!(geoBits.value & 1))
const showModels = computed(() => !!(geoBits.value & 2))
const showHelpers = computed(() => !!(geoBits.value & 4))
function toggleGeo(bit: number) { geoBits.value ^= bit; geoBits.commit() }
const collision = new THREE.Group() // collision mesh parts and shapes
world.add(collision)
const camera = new THREE.PerspectiveCamera(60, 1, 0.1, 6000)
const hemi = new THREE.HemisphereLight(0xdfe8ff, 0x3a3226, 1.1)
scene.add(hemi)
const sun = new THREE.DirectionalLight(0xffffff, 1.6)
sun.position.set(0.45, 1, 0.3)
scene.add(sun)
let dirty = true
const redraw = () => { dirty = true }

// mesh data (from /mapgeo/<guid>.bin, mapview.MeshBinary)
let V: Float32Array, I: Uint32Array, MASK: Uint16Array, FLAGS: Uint16Array, MAT: Uint16Array
let NY: Float32Array // per triangle: normal y (|n|=1), for the surface colours
let bounds = { min: [0, 0, 0], max: [0, 0, 0] }
type Part = { mesh: THREE.Mesh; tris: Uint32Array }
type PartKey = 'solid' | 'clip' | 'other'
const PARTS: PartKey[] = ['solid', 'clip', 'other']
const parts: Record<PartKey, Part | null> = { solid: null, clip: null, other: null }
const meshMat = new THREE.MeshLambertMaterial({ vertexColors: true, flatShading: true, side: THREE.DoubleSide })
// hero-only clip volumes: translucent, the cursor goes through them
const clipMat = new THREE.MeshLambertMaterial({ vertexColors: true, flatShading: true, side: THREE.DoubleSide, transparent: true, opacity: 0.1, depthWrite: false })
// shapes
let shapes: any = null
const shapeGroups: Record<string, THREE.Object3D[]> = { hulls: [], capsules: [], spheres: [] }
const shapeRanges = new Map<THREE.Mesh, { kind: string; ids: number[]; solid: boolean }>()

const heroBlocks = (mask: number) => (mask & 0x18) !== 0
// Mesh parts by triangle mask (mapview.TriPart): solid = blocks heroes and other categories; clip = heroes only
// (mask ⊆ 0x001A: invisible boxes, e.g. Anubis' roof blockers); other = no hero category (0x08 / 0x10).
const partOf = (mask: number): PartKey => ((mask & 0x18) === 0 ? 'other' : (mask & ~0x1a) === 0 ? 'clip' : 'solid')
const PART_BIT: Record<PartKey, number> = { solid: 1, clip: 2, other: 4 }

// Colours: surface = by what heroes collide with and the slope; height = ramp over the mesh bounds; mask /
// material = one colour per distinct value (legend).
const SURF = {
  floor: [0xc2, 0xad, 0x86], wall: [0x86, 0x93, 0xa9], ceiling: [0x62, 0x6b, 0x80], steep: [0xc2, 0x84, 0x70],
  oFloor: [0x4f, 0x9c, 0x94], oWall: [0x3e, 0x72, 0x7d], clip: [0xe0, 0x6a, 0x9a],
}
const RAMP = [[0x2c, 0x3e, 0x8c], [0x21, 0x90, 0x8d], [0x5e, 0xc9, 0x62], [0xe0, 0xd0, 0x4a], [0xf2, 0x7a, 0x3e]]
function hashColor(x: number): number[] {
  const h = ((x * 2654435761) >>> 0) / 4294967296
  const c = new THREE.Color().setHSL(h, 0.55, 0.58)
  return [c.r * 255, c.g * 255, c.b * 255]
}
function triColor(t: number, mode: number): number[] {
  if (mode === 1) {
    const y = (V[3 * I[3 * t]! + 1]! + V[3 * I[3 * t + 1]! + 1]! + V[3 * I[3 * t + 2]! + 1]!) / 3
    const f = Math.min(1, Math.max(0, (y - bounds.min[1]!) / Math.max(1, bounds.max[1]! - bounds.min[1]!))) * (RAMP.length - 1)
    const k = Math.min(RAMP.length - 2, Math.floor(f)), u = f - k
    return [0, 1, 2].map((c) => RAMP[k]![c]! * (1 - u) + RAMP[k + 1]![c]! * u)
  }
  if (mode === 2) return hashColor(MASK[t]!)
  if (mode === 3) return hashColor(MAT[t]! + 7919)
  const ny = NY[t]!
  const part = partOf(MASK[t]!)
  if (part === 'other') return Math.abs(ny) > 0.7 ? SURF.oFloor : SURF.oWall
  if (part === 'clip') return SURF.clip
  if (ny > 0.7) return (FLAGS[t]! & 1) ? SURF.floor : SURF.steep
  if (ny < -0.7) return SURF.ceiling
  return SURF.wall
}
const legendTick = ref(0) // recompute the legend once the mesh is loaded
const legendRows = computed(() => {
  legendTick.value
  if (colorMode.value !== 2 && colorMode.value !== 3) return []
  if (!MASK) return []
  const cnt = new Map<number, number>()
  const src = colorMode.value === 2 ? MASK : MAT
  for (let t = 0; t < src.length; t++) cnt.set(src[t]!, (cnt.get(src[t]!) ?? 0) + 1)
  return [...cnt.entries()].sort((a, b) => b[1] - a[1]).slice(0, 14).map(([k, n]) => {
    const c = colorMode.value === 2 ? hashColor(k) : hashColor(k + 7919)
    return { k: colorMode.value === 2 ? '0x' + k.toString(16) : String(k), n, c: `rgb(${c.map(Math.round).join(',')})`,
      hero: colorMode.value === 2 && heroBlocks(k) }
  })
})

function paint() {
  for (const k of PARTS) {
    const part = parts[k]
    if (!part) continue
    const col = part.mesh.geometry.getAttribute('color') as THREE.BufferAttribute
    const a = col.array as Uint8Array
    for (let j = 0; j < part.tris.length; j++) {
      const c = triColor(part.tris[j]!, colorMode.value)
      for (let q = 0; q < 3; q++) { a[9 * j + 3 * q] = c[0]!; a[9 * j + 3 * q + 1] = c[1]!; a[9 * j + 3 * q + 2] = c[2]! }
    }
    col.needsUpdate = true
  }
  redraw()
}
watch(colorMode, paint)
function setColor(k: number) { colorMode.value = k; colorMode.commit() }

async function loadMesh() {
  const t0 = performance.now()
  const r = await fetch(`/mapgeo/${p.guid}.bin`)
  if (!r.ok) throw new Error(`mesh: ${r.status} ${await r.text()}`)
  const buf = await r.arrayBuffer()
  const h = new Uint32Array(buf, 0, 4)
  if (h[0] !== 0x4d43574f || h[1] !== 1) throw new Error('mesh: bad header')
  const nv = h[2]!, nt = h[3]!
  V = new Float32Array(buf, 16, 3 * nv)
  I = new Uint32Array(buf, 16 + 12 * nv, 3 * nt)
  const o = 16 + 12 * nv + 12 * nt
  MASK = new Uint16Array(buf, o, nt)
  FLAGS = new Uint16Array(buf, o + 2 * nt, nt)
  MAT = new Uint16Array(buf, o + 4 * nt, nt)
  NY = new Float32Array(nt)
  for (let t = 0; t < nt; t++) {
    const a = 3 * I[3 * t]!, b = 3 * I[3 * t + 1]!, c = 3 * I[3 * t + 2]!
    const ux = V[b]! - V[a]!, uy = V[b + 1]! - V[a + 1]!, uz = V[b + 2]! - V[a + 2]!
    const wx = V[c]! - V[a]!, wy = V[c + 1]! - V[a + 1]!, wz = V[c + 2]! - V[a + 2]!
    const nx = uy * wz - uz * wy, ny = uz * wx - ux * wz, nz = ux * wy - uy * wx
    NY[t] = ny / (Math.hypot(nx, ny, nz) || 1)
    info[partOf(MASK[t]!)]++
  }
  info.tris = nt; info.verts = nv
  const lists = { solid: new Uint32Array(info.solid), clip: new Uint32Array(info.clip), other: new Uint32Array(info.other) }
  const at = { solid: 0, clip: 0, other: 0 }
  for (let t = 0; t < nt; t++) { const k = partOf(MASK[t]!); lists[k][at[k]++] = t }
  for (const k of PARTS) {
    const tris = lists[k]
    const pos = new Float32Array(9 * tris.length)
    for (let j = 0; j < tris.length; j++) {
      const t = tris[j]!
      for (let q = 0; q < 3; q++) {
        const v = 3 * I[3 * t + q]!
        pos[9 * j + 3 * q] = V[v]!; pos[9 * j + 3 * q + 1] = V[v + 1]!; pos[9 * j + 3 * q + 2] = V[v + 2]!
      }
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(new Uint8Array(9 * tris.length), 3, true))
    g.computeBoundingSphere()
    const m = new THREE.Mesh(g, k === 'clip' ? clipMat : meshMat)
    if (k === 'clip') m.renderOrder = 1
    m.visible = layers[k]
    collision.add(m)
    parts[k] = { mesh: m, tris }
  }
  paint()
  legendTick.value++
  return performance.now() - t0
}

function shapeMesh(geoms: THREE.BufferGeometry[], ids: number[], kind: string, color: string, solid: boolean) {
  if (!geoms.length) return
  const g = mergeGeometries(geoms, true)
  const m = new THREE.Mesh(g, new THREE.MeshLambertMaterial({ color, flatShading: true, side: THREE.DoubleSide,
    transparent: !solid, opacity: solid ? 1 : 0.45, depthWrite: solid }))
  shapeRanges.set(m, { kind, ids, solid })
  shapeGroups[kind]!.push(m)
  m.visible = (layers as any)[kind]
  collision.add(m)
}

function buildShapes(sh: any) {
  // Hulls: faces are vertex loops (fan-triangulated). Solid = blocks heroes (mask & 0x18), else translucent.
  const hullG: Record<string, THREE.BufferGeometry[]> = { s: [], t: [] }, hullI: Record<string, number[]> = { s: [], t: [] }
  sh.hulls.forEach((h: any, k: number) => {
    const pos: number[] = []
    for (const f of h.faces) for (let j = 1; j + 1 < f.length; j++) for (const v of [f[0], f[j], f[j + 1]]) pos.push(...h.verts[v])
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.Float32BufferAttribute(pos, 3))
    const key = !h.passThrough && heroBlocks(h.mask) ? 's' : 't'
    hullG[key]!.push(g); hullI[key]!.push(k)
  })
  const capG: Record<string, THREE.BufferGeometry[]> = { s: [], t: [] }, capI: Record<string, number[]> = { s: [], t: [] }
  sh.capsules.forEach((c: any, k: number) => {
    const a = new THREE.Vector3(...c.p0), b = new THREE.Vector3(...c.p1)
    const len = a.distanceTo(b)
    const g = new THREE.CapsuleGeometry(c.radius, len, 3, 10).toNonIndexed()
    const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), len > 1e-6 ? b.clone().sub(a).normalize() : new THREE.Vector3(0, 1, 0))
    g.applyMatrix4(new THREE.Matrix4().compose(a.clone().add(b).multiplyScalar(0.5), q, new THREE.Vector3(1, 1, 1)))
    const key = !c.passThrough && heroBlocks(c.mask) ? 's' : 't'
    capG[key]!.push(g); capI[key]!.push(k)
  })
  const sphG: Record<string, THREE.BufferGeometry[]> = { s: [], t: [] }, sphI: Record<string, number[]> = { s: [], t: [] }
  sh.spheres.forEach((c: any, k: number) => {
    const g = new THREE.SphereGeometry(c.radius, 14, 10).toNonIndexed()
    g.translate(c.center[0], c.center[1], c.center[2])
    const key = !c.passThrough && heroBlocks(c.mask) ? 's' : 't'
    sphG[key]!.push(g); sphI[key]!.push(k)
  })
  for (const key of ['s', 't']) {
    shapeMesh(hullG[key]!, hullI[key]!, 'hulls', '#e0955a', key === 's')
    shapeMesh(capG[key]!, capI[key]!, 'capsules', '#b58cf0', key === 's')
    shapeMesh(sphG[key]!, sphI[key]!, 'spheres', '#7fd68a', key === 's')
  }
}
watch(() => [layers.solid, layers.clip, layers.other, layers.hulls, layers.capsules, layers.spheres, layers.wire], () => {
  for (const k of PARTS) if (parts[k]) parts[k]!.mesh.visible = layers[k]
  for (const k of ['hulls', 'capsules', 'spheres']) for (const o of shapeGroups[k]!) o.visible = (layers as any)[k]
  meshMat.wireframe = clipMat.wireframe = layers.wire
  redraw()
})

// ---- placements: markers (front: depth tested; ghosts: the occluded part, faint), facing lines, selection ----
// ---- visual geometry (T064): the placed models (record types 1, 2, 8) with their looks' colour textures, one
// InstancedMesh per model + look, drawn at the model's finest LOD; loaded the first time the layer is switched on
const visual = new THREE.Group()
visual.visible = false
world.add(visual)
const helpers = new THREE.Group() // the helper / editor parts of the same batches
helpers.visible = false
world.add(helpers)
const hidden = new THREE.MeshBasicMaterial({ visible: false })
const vinfo = reactive({ state: '', batches: 0, done: 0, instances: 0, tris: 0, failed: 0, helpers: 0 })
const vtex = new Map<string, THREE.Texture>()
// texture size of the models layer: the largest mip ≤ this many pixels. Starts at 256 on every open (not remembered);
// raising it swaps the textures of the loaded models (T064). GPU memory grows ~4× per step (Anubis: 187 textures,
// ~65 MB at 256, ~260 MB at 512, ~1 GB at 1024).
const TEX_SIZES = [256, 512, 1024, 4096]
const texSize = ref(256)
function setTexSize(n: number) {
  if (n === texSize.value) return
  texSize.value = n
  for (const im of [...visual.children, ...helpers.children] as THREE.InstancedMesh[]) {
    for (const m of (im.userData.simple ?? im.material) as THREE.MeshLambertMaterial[]) {
      const d = m.userData as { color?: string; opacity?: string }
      if (d.color) m.map = materialTexture(vtex, d.color, n, redraw)
      if (d.opacity) m.alphaMap = materialTexture(vtex, d.opacity, n, redraw)
      m.needsUpdate = true
    }
  }
  // free the other sizes' textures (GPU memory)
  for (const [k, t] of vtex) if (!k.endsWith('/' + n)) { t.dispose(); vtex.delete(k) }
  if (pbrMode.value) { resetPBR(); applyMaterials() } // PBR maps follow the size, capped at 512
  redraw()
}

// ---- material (PBR) mode (T071): each slot's game material through the shared builder (utils/owmat.ts): one
// three.js material per game material, textures at min(texture size, 512) px (colour, normal, packed AO / roughness /
// metalness "orm", emissive, opacity: only what the builder uses), MeshStandardMaterial. Lighting: the generated studio
// environment + the sun (the hemisphere light is off), not the game's probes, lightmaps or GI. Default simple, remembered.
// off on every open (user, 2026-09-29): material shading, shadows and reflections are opt-in per session, not remembered
const sessionFlag = (v: number) => { const r = ref(v) as any; r.commit = () => {}; return r as typeof r & { commit: () => void } }
const pbrMode = sessionFlag(0)
const pbr = reactive({ previews: 0, materials: 0, pending: 0, converting: 0 })
const mbuilder = new MaterialBuilder(() => { pbr.pending = mbuilder.pending; redraw() })
const pbrMats = new Map<string, THREE.Material | null>()
const mPreviews = new Map<string, Preview>()
let envTex: THREE.Texture | null = null
const pbrSize = () => Math.min(texSize.value, 512)
function resetPBR() {
  pbrMats.forEach((m) => m?.dispose())
  pbrMats.clear()
  mbuilder.dispose()
  pbr.materials = 0
}
function pbrMaterial(guid: string): THREE.Material | null | undefined {
  if (!guid) return undefined
  if (pbrMats.has(guid)) return pbrMats.get(guid)
  const pv = mPreviews.get(guid)
  if (!pv) return undefined
  const m = mbuilder.build(guid, pv, { size: pbrSize(), context: 'map', orm: true, uv1: true })
  if (m && probes) patchProbes(m, probes)
  pbrMats.set(guid, m)
  pbr.materials = pbrMats.size
  return m
}
async function fetchPreviews(guids: string[]) {
  const want = [...new Set(guids.filter((g) => g && !mPreviews.has(g)))]
  for (let i = 0; i < want.length; i += 64) {
    try {
      const r = await api.MaterialPreviews(want.slice(i, i + 64))
      for (const [k, v] of Object.entries(r)) mPreviews.set(k, v as Preview)
    } catch { /* those slots stay simple */ }
    pbr.previews = mPreviews.size
  }
}
// the mesh's material array in PBR mode (a slot without a preview keeps its simple material)
function pbrArray(im: THREE.InstancedMesh) {
  const d = im.userData
  return (d.simple as THREE.Material[]).map((sm, k) => {
    if (sm === hidden) return hidden
    const m = pbrMaterial(d.slotMats[k])
    return m === null ? hidden : m ?? sm
  })
}
// nearest-first conversion, a few meshes per frame so the UI stays responsive
let convertQueue: THREE.InstancedMesh[] = []
let converting = false
async function applyMaterials() {
  const all = [...visual.children, ...helpers.children] as THREE.InstancedMesh[]
  if (!pbrMode.value) {
    convertQueue = []
    for (const im of all) if (im.userData.simple) im.material = im.userData.simple
    applyLighting()
    return
  }
  pbrLighting()
  await fetchPreviews(all.flatMap((im) => im.userData.slotMats ?? []))
  convertQueue = all.slice()
  pump()
}
function pbrLighting() { applyLighting() }

// ---- lighting (T071 round 3): "studio" (T064 / round 2: hemisphere + white sun, or a studio environment in material
// mode) or "map": the map's own environment (utils/owenv.ts): its sun direction and HDR colour with a shadow map over
// the playable area, the sky cube as background and image-based lighting (material mode) or its ambient cube as a
// hemisphere light (simple mode), ACES tone mapping with an exposure, and the map's colour-grading LUT. Still not the
// game's lighting: no baked GI / lightmaps / probes / AO (Rendering.md §8). Remembered: map3d.light / .grade /
// .exposure.
const lightMode = usePanel('map3d.light', 1)   // 1 map, 0 studio
const shadowsOn = sessionFlag(0)
const gradeOn = usePanel('map3d.grade', 1)
const reflOn = sessionFlag(0) // reflection probes (material mode, map lighting)
let probes: Probes | null = null
const mirrorTest = ref(false) // every surface a perfect mirror (not remembered)
const toneMode = usePanel('map3d.tone', 0)   // 0 ACES, 1 Neutral (Khronos PBR Neutral), 2 AgX
const TONES = [THREE.ACESFilmicToneMapping, THREE.NeutralToneMapping, THREE.AgXToneMapping]
const exposure = usePanel('map3d.exposure', 40) // percent; 0.40 matches the sun (HDR ≈ 3) best by eye (T071)
function setPanel(c: any, v: number) { c.value = v; c.commit() }
const panels: Record<string, any> = { light: lightMode, shadows: shadowsOn, grade: gradeOn, exposure, tone: toneMode, refl: reflOn }
function setLight(k: string, v: number) { setPanel(panels[k], v) }
function slideExposure(v: number, commit = false) { exposure.value = v; if (commit) exposure.commit() }
const bgColor = new THREE.Color('#0e131e')
let menv: MapEnv | null = null
const envState = reactive({ state: '', sun: '', lut: false, sky: false, probes: 0 })
let composer: EffectComposer | null = null
let lutPass: LUTPass | null = null
scene.add(sun.target)
function mapLit() { return !!lightMode.value && !!menv }
function placeSun() {
  if (!mapLit()) { sun.position.set(0.45, 1, 0.3); sun.target.position.set(0, 0, 0); return }
  const c = new THREE.Vector3((bounds.min[0] + bounds.max[0]) / 2, (bounds.min[1] + bounds.max[1]) / 2, (bounds.min[2] + bounds.max[2]) / 2)
  const R = Math.max(30, 0.5 * Math.hypot(bounds.max[0] - bounds.min[0], bounds.max[1] - bounds.min[1], bounds.max[2] - bounds.min[2]) + 10)
  world.localToWorld(c)
  const d = menv!.sunDir.clone()
  if (p.mirror) d.x = -d.x
  sun.target.position.copy(c)
  sun.position.copy(c).addScaledVector(d, -2 * R)
  const sc = sun.shadow.camera as THREE.OrthographicCamera
  sc.left = -R; sc.right = R; sc.top = R; sc.bottom = -R; sc.near = 1; sc.far = 4 * R
  sc.updateProjectionMatrix()
  sun.shadow.bias = -0.0002
  sun.shadow.normalBias = 0.04
  shadowDirty()
}
function shadowDirty() { if (renderer) renderer.shadowMap.needsUpdate = true; redraw() }
function applyLighting() {
  if (!renderer) return
  const pbr = !!pbrMode.value, mapL = mapLit()
  if (mapL) {
    scene.background = menv!.sky ?? bgColor
    scene.environment = menv!.env
    scene.environmentIntensity = 1
    sun.color.copy(menv!.sunColor)
    sun.intensity = 1
    // hemisphere: in material mode only the warm bounce from below (the environment already gives the sky light);
    // in simple mode (Lambert ignores the environment) the ambient cube's halves + the bounce
    hemi.visible = true
    if (pbr) { hemi.color.setRGB(0, 0, 0); hemi.groundColor.copy(menv!.bounce) }
    else { hemi.color.copy(menv!.hemiSky); hemi.groundColor.copy(menv!.hemiGround).add(menv!.bounce) }
    hemi.intensity = Math.PI
  } else {
    scene.background = bgColor
    sun.color.set(0xffffff)
    hemi.color.set(0xdfe8ff); hemi.groundColor.set(0x3a3226); hemi.intensity = 1.1
    if (pbr) {
      if (!envTex) envTex = studioEnvironment(renderer)
      scene.environment = envTex; scene.environmentIntensity = 0.45; hemi.visible = false; sun.intensity = 0.9
    } else { scene.environment = null; hemi.visible = true; sun.intensity = 1.6 }
  }
  const shadows = mapL && !!shadowsOn.value
  if (renderer.shadowMap.enabled !== shadows || renderer.toneMapping !== (mapL ? TONES[toneMode.value] ?? TONES[0] : THREE.NoToneMapping)) {
    renderer.shadowMap.enabled = shadows
    renderer.toneMapping = mapL ? TONES[toneMode.value] ?? TONES[0] : THREE.NoToneMapping
    scene.traverse((o: any) => { for (const m of [o.material].flat()) if (m) m.needsUpdate = true })
  }
  renderer.toneMappingExposure = exposure.value / 100
  sun.castShadow = shadows
  if (lutPass) lutPass.enabled = mapL && !!gradeOn.value && !!menv?.lut
  if (probes) {
    probes.uniforms.owProbeOn.value = mapL && reflOn.value ? (mirrorTest.value ? 2 : 1) : 0
    world.updateMatrixWorld()
    ;(probes.uniforms.owGameFromWorld.value as THREE.Matrix4).copy(world.matrixWorld).invert()
  }
  placeSun()
  redraw()
}
async function initMapLighting() {
  if (!renderer) return
  envState.state = 'loading'
  renderer.shadowMap.type = THREE.PCFSoftShadowMap
  renderer.shadowMap.autoUpdate = false
  const size = Math.min(4096, renderer.capabilities.maxTextureSize)
  sun.shadow.mapSize.set(size, size)
  try {
    menv = await loadMapEnvironment(renderer, p.guid)
    if (unmounted) { disposeMapEnvironment(menv); return }
    const sc = menv.sunColor
    envState.sun = `${sc.r.toFixed(2)}, ${sc.g.toFixed(2)}, ${sc.b.toFixed(2)}`
    envState.lut = !!menv.lut; envState.sky = !!menv.sky
    const rt = new THREE.WebGLRenderTarget(1, 1, { type: THREE.HalfFloatType, samples: 4 })
    composer = new EffectComposer(renderer, rt)
    composer.addPass(new RenderPass(scene, camera))
    composer.addPass(new OutputPass())
    if (menv.lut) { lutPass = new LUTPass({ lut: menv.lut, intensity: 1 }); composer.addPass(lutPass) }
    resize()
    envState.state = 'ok'
    try { // reflection probes: patch the material-mode materials built so far; later ones are patched when built
      probes = await loadProbes(p.guid)
      if (probes && !unmounted) {
        envState.probes = probes.count - 1
        pbrMats.forEach((m) => { if (m) patchProbes(m, probes!) })
      }
    } catch (e: any) { envState.probes = -1 }
  } catch (e: any) { envState.state = 'error: ' + String(e?.message ?? e) }
  applyLighting()
}
// the frame: through the composer (tone mapping, LUT) with map lighting, else straight
function renderScene() {
  if (mapLit() && composer) composer.render()
  else renderer!.render(scene, camera)
}
watch([lightMode, shadowsOn, gradeOn, exposure, toneMode, reflOn, mirrorTest], applyLighting)
function enqueuePBR(im: THREE.InstancedMesh) {
  fetchPreviews(im.userData.slotMats).then(() => {
    patchSimple(im)
    if (!pbrMode.value) return
    pbrLighting()
    convertQueue.push(im)
    pump()
  })
}
// simple mode: layered materials (0x25 layer B by vertex colour, 0x24 overlay) get the same shader patch as the PBR ones
function patchSimple(im: THREE.InstancedMesh) {
  const d = im.userData
  ;(d.simple as THREE.Material[]).forEach((m, k) => {
    if (m === hidden || !d.slotMats[k] || m.userData.owPatched) return
    if (patchLayers(m, d.slotMats[k], mPreviews.get(d.slotMats[k]), mbuilder, pbrSize())) { m.userData.owPatched = true; redraw() }
  })
}
function pump() {
  if (converting) return
  converting = true
  const step = () => {
    if (!pbrMode.value || !convertQueue.length || unmounted) { converting = false; pbr.converting = 0; return }
    const cam = world.worldToLocal(camera.position.clone())
    const t0 = performance.now()
    while (convertQueue.length && performance.now() - t0 < 8) { // ≤ 8 ms of work per frame
      let best = 0, bd = Infinity
      convertQueue.forEach((im, i) => { const s = im.boundingSphere; const d = s ? s.center.distanceTo(cam) - s.radius : 0; if (d < bd) { bd = d; best = i } })
      const im = convertQueue.splice(best, 1)[0]
      im.material = pbrArray(im)
    }
    pbr.converting = convertQueue.length
    redraw()
    requestAnimationFrame(step)
  }
  requestAnimationFrame(step)
}
function setPBRMode(on: number) { if (pbrMode.value === on) return; pbrMode.value = on; pbrMode.commit(); applyMaterials() }

// frame time (T071): for 3 s, frames are rendered back to back (not paced by the display's refresh). Each frame ends
// with a 1-pixel readPixels, which waits for the GPU (gl.finish() doesn't block in WebKit's WebGL). The result is the
// GPU-synced time per frame and the rate it allows, capped at 160 fps. setTimeout's own latency (~9 ms per call in
// WebKit) is left out: it would cap the loop at ~70 fps. The view as it is now.
const BENCH_CAP = 160
const bench = reactive({ ms: 0, fps: 0, frames: 0, mode: '', running: false, capped: false })
const px1 = new Uint8Array(4)
function measureFrames() {
  if (!renderer || bench.running) return
  bench.running = true
  const gl = renderer.getContext()
  const start = performance.now(), until = start + 3000
  let n = 0, work = 0
  const one = () => {
    if (!renderer || unmounted) { bench.running = false; return }
    const t0 = performance.now()
    renderScene()
    gl.readPixels(0, 0, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, px1)
    const t1 = performance.now()
    work += t1 - t0
    n++
    if (t1 < until) { setTimeout(one, 0); return }
    const ms = work / n, fps = Math.min(BENCH_CAP, 1000 / ms)
    Object.assign(bench, { ms: +ms.toFixed(2), fps: +fps.toFixed(1), frames: n, running: false,
      mode: pbrMode.value ? 'material' : 'simple', capped: 1000 / ms >= BENCH_CAP })
    if (ex.extra.map) ex.extra.map.frameTime = { msPerFrame: bench.ms, fps: bench.fps, capped: bench.capped, frames: n, mode: bench.mode, materials: pbr.materials, pendingTextures: mbuilder.pending }
    LogPrint(`map3d ${p.guid}: ${bench.mode} ${bench.ms} ms/frame GPU-synced, ${bench.fps} fps${bench.capped ? ' (at the 160 cap)' : ''} over ${n} frames in 3 s, ${pbr.materials} PBR materials, ${mbuilder.pending} textures pending`)
    redraw()
  }
  one()
}
let visualStarted = false
let unmounted = false
async function loadVisual() {
  if (visualStarted) return
  visualStarted = true
  vinfo.state = 'loading'
  try {
    const v = await api.MapVisual(p.guid)
    vinfo.batches = v.batches.length; vinfo.instances = v.instances; vinfo.tris = v.triangles
    const m4 = new THREE.Matrix4(), q = new THREE.Quaternion(), pos = new THREE.Vector3(), sc = new THREE.Vector3()
    const queue = v.batches.filter((b: any) => !b.error)
    vinfo.failed = v.batches.length - queue.length
    // nearest first (T071): each worker takes the batch whose nearest instance is closest to the camera now
    for (const b of queue) {
      const c: THREE.Vector3[] = []
      for (let k = 0; k < b.records.length; k++) c.push(new THREE.Vector3(b.instances[10 * k], b.instances[10 * k + 1], b.instances[10 * k + 2]))
      b._pts = c
    }
    const next = () => {
      if (!queue.length) return undefined
      const cam = world.worldToLocal(camera.position.clone())
      let best = 0, bd = Infinity
      queue.forEach((b: any, i: number) => { for (const q of b._pts) { const d = q.distanceToSquared(cam); if (d < bd) { bd = d; best = i } } })
      return queue.splice(best, 1)[0]
    }
    const worker = async () => {
      for (let b = next(); b; b = next()) {
        try {
          const mesh = await fetchMesh(b.model, b.lod)
          if (unmounted) { mesh.geo.dispose(); return }
          const real = (g: any) => {
            if (b.decals?.[g.material]) return hidden // opacity without colour (a normal-only decal)
            const t = b.colors?.[g.material], o = b.opacity?.[g.material]
            const opts: any = t ? { map: materialTexture(vtex, t, texSize.value, redraw), side: THREE.DoubleSide } : { color: 0x9a9a9a, side: THREE.DoubleSide }
            if (o) Object.assign(opts, { alphaMap: materialTexture(vtex, o, texSize.value, redraw), transparent: true, depthWrite: false })
            const mat = new THREE.MeshLambertMaterial(opts)
            mat.userData = { color: t, opacity: o }
            return mat
          }
          const isHelper = (g: any) => !!b.helpers?.[g.material]
          const n = b.records.length
          for (const wantHelper of [false, true]) {
            if (!mesh.groups.some((g) => isHelper(g) === wantHelper)) continue
            const im = new THREE.InstancedMesh(mesh.geo, mesh.groups.map((g) => (isHelper(g) === wantHelper ? real(g) : hidden)), n)
            for (let k = 0; k < n; k++) {
              const f = b.instances.slice(10 * k, 10 * k + 10)
              pos.set(f[0], f[1], f[2]); q.set(f[3], f[4], f[5], f[6]); sc.set(f[7], f[8], f[9])
              im.setMatrixAt(k, m4.compose(pos, q, sc))
            }
            im.instanceMatrix.needsUpdate = true
            im.computeBoundingSphere()
            // instance index inside its record (model groups place many)
            const seen = new Map<number, number>()
            const instIdx = b.records.map((r: number) => { const i = seen.get(r) ?? 0; seen.set(r, i + 1); return i })
            im.userData = { model: b.model, look: b.look, records: b.records, instances: b.instances, helper: wantHelper, instIdx,
              simple: im.material, slotMats: mesh.groups.map((g) => (b.materials?.[g.material] ?? '')) }
            applyHiddenTo(im)
            im.castShadow = im.receiveShadow = true
            ;(wantHelper ? helpers : visual).add(im)
            shadowDirty()
            enqueuePBR(im)
            if (wantHelper) vinfo.helpers += n
          }
        } catch { vinfo.failed++ }
        vinfo.done++
        redraw()
      }
    }
    await Promise.all([worker(), worker(), worker(), worker()])
    vinfo.state = 'done'
  } catch (e: any) { vinfo.state = 'error: ' + String(e?.message ?? e) }
}
watch([showModels, showHelpers], ([on, hon]) => { visual.visible = on; helpers.visible = hon; if (on || hon) loadVisual(); shadowDirty() }, { immediate: true })
watch(showCol, (on) => { collision.visible = on; redraw(); hoverSoon() }, { immediate: true })

// ---- hiding models (T064): hidden instances get a zero matrix (not drawn, not pickable); the list in the panel
// unhides them. Kept for the session (per map view, not remembered).
type HiddenEntry = { key: string; kind: 'instance' | 'record' | 'model'; record?: number; instance?: number; model?: string; look?: string; label: string }
const hiddenList = ref<HiddenEntry[]>([])
function isHidden(model: string, rec: number, inst: number) {
  return hiddenList.value.some((h) => (h.kind === 'model' && h.model === model) || (h.kind === 'record' && h.record === rec) ||
    (h.kind === 'instance' && h.record === rec && h.instance === inst))
}
const zero = new THREE.Matrix4().makeScale(0, 0, 0)
function applyHiddenTo(im: THREE.InstancedMesh) {
  const d = im.userData as any, m4 = new THREE.Matrix4(), q = new THREE.Quaternion(), pos = new THREE.Vector3(), sc = new THREE.Vector3()
  for (let k = 0; k < d.records.length; k++) {
    if (isHidden(d.model, d.records[k], d.instIdx[k])) im.setMatrixAt(k, zero)
    else {
      const f = d.instances.slice(10 * k, 10 * k + 10)
      pos.set(f[0], f[1], f[2]); q.set(f[3], f[4], f[5], f[6]); sc.set(f[7], f[8], f[9])
      im.setMatrixAt(k, m4.compose(pos, q, sc))
    }
  }
  im.instanceMatrix.needsUpdate = true
  im.computeBoundingSphere()
}
function applyHidden() {
  for (const im of [...visual.children, ...helpers.children] as THREE.InstancedMesh[]) applyHiddenTo(im)
  buildHighlight()
  if (ex.extra.map) ex.extra.map.hidden = hiddenList.value.map(({ kind, record, instance, model, look }) => ({ kind, record, instance, model, look }))
  shadowDirty()
}
const shortG = (g?: string) => (g ? g.slice(4).replace(/^0+/, '') || '0' : '?')
// hide the selection: 'selection' = the picked instance (or the whole record when none was picked), 'model' = every
// placement of the selected model
function hideSelection(what: 'selection' | 'model' = 'selection') {
  if (p.sel < 0) return
  const x = p.v?.placements?.[p.sel]
  const model = p.selInst?.model ?? (x?.guid && x.guid.startsWith('0d') ? x.guid : undefined)
  let e: HiddenEntry
  if (what === 'model') {
    if (!model) return
    e = { key: 'm:' + model, kind: 'model', model, label: `model ${shortG(model)} (everywhere)` }
  } else if (p.selInst && p.selInst.record === p.sel) {
    e = { key: `i:${p.sel}:${p.selInst.instance}`, kind: 'instance', record: p.sel, instance: p.selInst.instance, model: p.selInst.model, look: p.selInst.look,
      label: `#${p.sel} instance ${p.selInst.instance} · model ${shortG(p.selInst.model)}` }
  } else {
    e = { key: 'r:' + p.sel, kind: 'record', record: p.sel, model, label: `#${p.sel} ${markerName(x ?? {})}` }
  }
  if (!hiddenList.value.some((h) => h.key === e.key)) hiddenList.value = [...hiddenList.value, e]
  applyHidden()
  emit('select', -1)
}
function unhide(key: string) { hiddenList.value = hiddenList.value.filter((h) => h.key !== key); applyHidden() }
function unhideAll() { hiddenList.value = []; applyHidden() }

// picking a model: the instance under a ray (three.js raycast of the InstancedMeshes: bounding sphere per instance,
// then its triangles)
function pickModel(r: THREE.Raycaster) {
  const objs = [...(showModels.value ? visual.children : []), ...(showHelpers.value ? helpers.children : [])]
  if (!objs.length) return null
  // the first hit on a drawn group (a mesh's other groups use the invisible material)
  const h = r.intersectObjects(objs, false).find((x) => {
    const m = (x.object as THREE.Mesh).material as THREE.Material[]
    return x.instanceId !== undefined && m[x.face?.materialIndex ?? 0]?.visible !== false
  })
  if (!h) return null
  const d = h.object.userData as any
  const rec = d.records[h.instanceId]
  // the instance's index inside its record (a model group has many)
  let inst = 0
  for (let k = 0; k < h.instanceId; k++) if (d.records[k] === rec) inst++
  const f = d.instances.slice(10 * h.instanceId, 10 * h.instanceId + 10)
  return { record: rec, instance: inst, model: d.model, look: d.look, pos: [f[0], f[1], f[2]].map(r2), dist: h.distance }
}
// highlight of the selected record's instances (tinted overlay; the picked instance also gets a box)
const hl = new THREE.Group()
world.add(hl)
const hlMat = new THREE.MeshBasicMaterial({ color: 0x5b9bff, transparent: true, opacity: 0.35, depthWrite: false, polygonOffset: true, polygonOffsetFactor: -2, polygonOffsetUnits: -2, side: THREE.DoubleSide })
function buildHighlight() {
  hl.children.slice().forEach((c: any) => { hl.remove(c); if (c.isLineSegments) c.geometry.dispose() })
  if (p.sel < 0 || !(showModels.value || showHelpers.value)) { redraw(); return }
  const m4 = new THREE.Matrix4()
  const shown = [...(showModels.value ? visual.children : []), ...(showHelpers.value ? helpers.children : [])]
  for (const im of shown as THREE.InstancedMesh[]) {
    const d = im.userData as any
    let n = -1
    d.records.forEach((rec: number, k: number) => {
      if (rec !== p.sel) return
      n++
      if (isHidden(d.model, rec, d.instIdx?.[k] ?? n)) return
      im.getMatrixAt(k, m4)
      const o = new THREE.Mesh(im.geometry, hlMat)
      o.matrixAutoUpdate = false
      o.matrix.copy(m4)
      hl.add(o)
      if (p.selInst?.record === rec && p.selInst?.instance === n) {
        im.geometry.computeBoundingBox()
        const b = new THREE.Box3Helper(im.geometry.boundingBox!.clone(), 0xffffff)
        b.matrixAutoUpdate = false
        b.matrix.copy(m4)
        hl.add(b)
      }
    })
  }
  redraw()
}
watch(() => [p.sel, p.selInst, showModels.value, showHelpers.value, vinfo.done], buildHighlight)

const markerVS = `
attribute vec3 color; attribute float size; uniform float uScale; varying vec3 vC;
void main() { vC = color; gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); gl_PointSize = size * uScale; }`
const markerFS = `
varying vec3 vC; uniform float uAlpha; uniform float uRing;
void main() {
  vec2 d = gl_PointCoord - 0.5; float r = dot(d, d);
  if (r > 0.25) discard;
  if (uRing > 0.5 && r < 0.12) discard;
  gl_FragColor = vec4(vC, uAlpha);
}`
const mkMat = (alpha: number, depth: boolean, ring = 0) => new THREE.ShaderMaterial({
  vertexShader: markerVS, fragmentShader: markerFS, transparent: alpha < 1 || ring > 0, depthTest: depth, depthWrite: false,
  uniforms: { uScale: { value: window.devicePixelRatio || 1 }, uAlpha: { value: alpha }, uRing: { value: ring } },
})
const emptyPoints = () => {
  const g = new THREE.BufferGeometry()
  g.setAttribute('position', new THREE.Float32BufferAttribute([], 3))
  g.setAttribute('color', new THREE.Float32BufferAttribute([], 3))
  g.setAttribute('size', new THREE.Float32BufferAttribute([], 1))
  return g
}
const markerGeo = emptyPoints()
const markers = new THREE.Points(markerGeo, mkMat(1, true))
const ghosts = new THREE.Points(markerGeo, mkMat(0.28, false))
ghosts.renderOrder = 2
markers.renderOrder = 3
const facing = new THREE.LineSegments(emptyPoints(), new THREE.LineBasicMaterial({ vertexColors: true }))
const selGeo = emptyPoints()
const selMark = new THREE.Points(selGeo, mkMat(1, false, 1))
selMark.renderOrder = 4
world.add(markers, ghosts, facing, selMark)
const ORDER = ['static', 'occluder', 'reflection_probe', 'light', 'effect', 'sound_volume', 'sound_emitter', 'placed_model',
  'prop', 'volume_0x44', 'volume_0x45', 'text_label', 'map_text', 'gameplay', 'mover', 'networked']
let markerList: any[] = [] // placements shown, in draw order
function buildMarkers() {
  const P = (p.v?.placements ?? []).filter((x: any) => x.hasPos && p.shown[x.class] !== false)
  // model groups' other instances (2D: the small static dots); picking one selects its group record
  if (p.shown.static !== false)
    for (const q of p.v?.points ?? []) {
      const o = p.v.placements[q.i]
      P.push({ i: q.i, class: 'static', pos: [q.x, q.y, q.z], yaw: 0, type: o?.type, name: o?.name, guid: o?.guid, instance: true })
    }
  const rank = (c: string) => { const i = ORDER.indexOf(c); return i < 0 ? 0 : i }
  P.sort((a: any, b: any) => rank(a.class) - rank(b.class))
  markerList = P
  const pos = new Float32Array(3 * P.length), col = new Float32Array(3 * P.length), size = new Float32Array(P.length)
  const fpos: number[] = [], fcol: number[] = []
  const c = new THREE.Color()
  P.forEach((x: any, k: number) => {
    pos.set(x.pos, 3 * k)
    const s = p.cls(x.class)
    c.set(s.c)
    col.set([c.r, c.g, c.b], 3 * k)
    size[k] = x.instance ? 2 * s.r + 1 : 2 * s.r + 3
    if (x.class === 'networked' || x.class === 'mover') {
      fpos.push(...x.pos, x.pos[0] + Math.sin(x.yaw) * 1.5, x.pos[1], x.pos[2] + Math.cos(x.yaw) * 1.5)
      fcol.push(c.r, c.g, c.b, c.r, c.g, c.b)
    }
  })
  markerGeo.setAttribute('position', new THREE.BufferAttribute(pos, 3))
  markerGeo.setAttribute('color', new THREE.BufferAttribute(col, 3))
  markerGeo.setAttribute('size', new THREE.BufferAttribute(size, 1))
  markerGeo.computeBoundingSphere()
  facing.geometry.dispose()
  facing.geometry = new THREE.BufferGeometry()
  facing.geometry.setAttribute('position', new THREE.Float32BufferAttribute(fpos, 3))
  facing.geometry.setAttribute('color', new THREE.Float32BufferAttribute(fcol, 3))
  buildSel()
  redraw()
}
function buildSel() {
  const x = p.v?.placements?.[p.sel]
  const on = x?.hasPos
  selGeo.setAttribute('position', new THREE.Float32BufferAttribute(on ? x.pos : [], 3))
  selGeo.setAttribute('color', new THREE.Float32BufferAttribute(on ? [1, 1, 1] : [], 3))
  selGeo.setAttribute('size', new THREE.Float32BufferAttribute(on ? [20] : [], 1))
  redraw()
}
watch(() => ({ ...p.shown }), buildMarkers, { deep: true })
watch(() => p.sel, buildSel)
watch(() => layers.ghosts, (g) => { ghosts.visible = g; redraw() }, { immediate: true })

// ---- camera: orbit (target, yaw, pitch, distance) or fly (position, yaw, pitch) ----
// Direction from the target to the camera: (cos p · sin y, sin p, cos p · cos y); fly looks the opposite way.
const cam = reactive({ target: new THREE.Vector3(), yaw: 0, pitch: 1.0, dist: 100, pos: new THREE.Vector3(), speed: 12 })
const back = () => new THREE.Vector3(Math.cos(cam.pitch) * Math.sin(cam.yaw), Math.sin(cam.pitch), Math.cos(cam.pitch) * Math.cos(cam.yaw))
function applyCamera() {
  if (camMode.value === 0) cam.pos.copy(cam.target).addScaledVector(back(), cam.dist)
  camera.position.copy(cam.pos)
  camera.lookAt(cam.pos.clone().sub(back()))
  camera.updateMatrixWorld()
  redraw()
  reportCamera()
  updateCamInfo()
  if (camMode.value === 2) hoverLine() // the status bar shows the camera position
}
// the camera's own position (game coordinates), always shown in a corner (T064 round 1); click copies it
const camInfo = reactive({ pos: [0, 0, 0] as number[], yaw: 0, pitch: 0 })
function updateCamInfo() {
  const c = camera.position
  camInfo.pos = [p.mirror ? -c.x : c.x, c.y, c.z]
  camInfo.yaw = cam.yaw * 180 / Math.PI
  camInfo.pitch = cam.pitch * 180 / Math.PI
}
function copyCam() { copy(camInfo.pos.map((x) => x.toFixed(2)).join(', '), 'Camera position') }
// scene (possibly mirrored) ↔ game coordinates
const toScene = (x: number[]) => new THREE.Vector3(p.mirror ? -x[0]! : x[0], x[1], x[2])
function fitAll() {
  // like the 2D plot: the central 98 % of the entities, seen from above (x → right, z → down)
  const ps = (p.v?.placements ?? []).filter((x: any) => x.hasPos && x.type === 11)
  let c = new THREE.Vector3((bounds.min[0]! + bounds.max[0]!) / 2, (bounds.min[1]! + bounds.max[1]!) / 2, (bounds.min[2]! + bounds.max[2]!) / 2)
  let span = Math.max(bounds.max[0]! - bounds.min[0]!, bounds.max[2]! - bounds.min[2]!)
  if (ps.length) {
    const q = (a: number[], t: number) => a.sort((u, w) => u - w)[Math.floor(t * (a.length - 1))]!
    const xs = ps.map((x: any) => x.pos[0]), ys = ps.map((x: any) => x.pos[1]), zs = ps.map((x: any) => x.pos[2])
    const x0 = q(xs, 0.01), x1 = q(xs, 0.99), z0 = q(zs, 0.01), z1 = q(zs, 0.99)
    c = new THREE.Vector3((x0 + x1) / 2, q(ys, 0.5), (z0 + z1) / 2)
    span = Math.max(x1 - x0, z1 - z0) + 20
  }
  cam.target.copy(toScene([c.x, c.y, c.z]))
  cam.yaw = 0
  cam.pitch = 1.05
  cam.dist = span * 0.95
  // the framing is computed as an orbit around the centre; the camera then starts in fly mode there (the default
  // mode at every open and after Reset, T064)
  cam.pos.copy(cam.target).addScaledVector(back(), cam.dist)
  if (camMode.value !== 1) { unlock(); camMode.value = 1; camMode.commit() }
  applyCamera()
}
function setCamMode(m: number) {
  if (m === camMode.value) return
  if (m === 2) { prevMode = camMode.value; if (capture.value) requestLock() } // a click or key is a user gesture: lock right away
  else unlock()
  if (m === 0) { // fly → orbit around the point under the screen centre (or 20 m ahead)
    cam.dist = centreHit ?? 20
    cam.target.copy(cam.pos).addScaledVector(back(), -cam.dist)
  }
  camMode.value = m
  camMode.commit()
  applyCamera()
}
// FPS noclip mode: the mouse is captured (Pointer Lock API) and looks around; Esc releases it, a click in the view
// captures it again. If the webview refuses the lock, the mode falls back to drag-to-look (lockFailed).
// FPS: pointer lock, or drag to look as the fallback (stored inverted: SetPanel drops values <= 0)
const noCapture = usePanel('map3d.nocapture', 0)
// FPS mouse sensitivity, 0.1×–10× on a log scale (1× = 0.0025 rad per pixel), default 3×. Stored as
// 1000 + 100·log10(×) because SetPanel keeps positive integers only.
const sensStored = usePanel('map3d.sens', 1048)
const sensLog = computed(() => (sensStored.value - 1000) / 100) // log10(×)
const sens = computed(() => Math.pow(10, sensLog.value))
function setSens(v: number) { sensStored.value = 1000 + Math.round(v * 100) }
function saveSens() { sensStored.commit() }
function resetSens() { sensStored.value = 1048; sensStored.commit(true) }
const capture = computed(() => !noCapture.value)
function setCapture(on: boolean) { noCapture.value = on ? 0 : 1; noCapture.commit(on); if (!on) unlock() }
const locked = ref(false)
const lockFailed = ref(false)
let lockReleasedAt = 0
let prevMode = 1
function requestLock() {
  const w = wrap.value as any
  if (!w?.requestPointerLock) { lockFailed.value = true; return }
  try {
    const r = w.requestPointerLock()
    if (r?.catch) r.catch(() => { lockFailed.value = true })
  } catch { lockFailed.value = true }
}
function unlock() { if (document.pointerLockElement) document.exitPointerLock() }
function lockChange() {
  const was = locked.value
  locked.value = !!wrap.value && document.pointerLockElement === wrap.value
  if (locked.value) lockFailed.value = false
  if (was && !locked.value) lockReleasedAt = performance.now()
  hoverLine()
}
function lockError() { lockFailed.value = true; hoverLine() }
let centreHit: number | null = null // distance to the mesh under the screen centre (updated after moves)
function focus(i: number) {
  const x = p.v?.placements?.[i]
  if (!x?.hasPos) return
  const t = toScene(x.pos)
  if (camMode.value === 0) {
    cam.target.copy(t)
    cam.dist = Math.min(cam.dist, 18)
  } else {
    cam.pos.copy(t).addScaledVector(back(), 12)
  }
  applyCamera()
}
defineExpose({ focus, fitAll, hideSelection })

// ---- input ----
const keys = new Set<string>()
let drag: null | { x: number; y: number; button: number; moved: boolean; shift: boolean } = null
function down(e: MouseEvent) {
  ex.focus = pane
  if (camMode.value === 2 && capture.value && !lockFailed.value) {
    drag = null
    if (!locked.value) requestLock()
    else if (e.button === 0) pickCentre() // captured: click selects the marker under the crosshair
    return
  }
  drag = { x: e.clientX, y: e.clientY, button: e.button, moved: false, shift: e.shiftKey }
}
function move(e: MouseEvent) {
  if (locked.value) { // FPS: mouse look, no roll, pitch clamped
    const k = 0.0025 * sens.value
    cam.yaw -= e.movementX * k
    cam.pitch = Math.max(-1.55, Math.min(1.55, cam.pitch + e.movementY * k))
    applyCamera()
    hoverSoon()
    return
  }
  mouse.x = e.clientX; mouse.y = e.clientY; mouse.in = true
  hoverSoon()
  if (!drag) return
  const dx = e.clientX - drag.x, dy = e.clientY - drag.y
  if (Math.abs(dx) + Math.abs(dy) > 2) drag.moved = true
  drag.x = e.clientX; drag.y = e.clientY
  const pan = camMode.value === 0 && (drag.button === 1 || drag.button === 2 || drag.shift)
  if (pan) {
    const k = cam.dist * 0.0018
    const right = new THREE.Vector3().setFromMatrixColumn(camera.matrixWorld, 0)
    const up = new THREE.Vector3().setFromMatrixColumn(camera.matrixWorld, 1)
    cam.target.addScaledVector(right, -dx * k).addScaledVector(up, dy * k)
  } else {
    const k = camMode.value === 0 ? 0.006 : camMode.value === 2 ? 0.0025 * sens.value : 0.004 // FPS drag: its sensitivity
    cam.yaw -= dx * k
    cam.pitch = Math.max(-1.55, Math.min(1.55, cam.pitch + dy * k)) // orbit: drag down = view from higher; fly: look down
  }
  applyCamera()
}
function up(e: MouseEvent) {
  if (drag && !drag.moved && drag.button === 0) pick(e)
  drag = null
}
function wheel(e: WheelEvent) {
  if (camMode.value !== 0) { // fly / FPS: speed
    cam.speed = Math.max(0.5, Math.min(400, cam.speed * Math.exp(-e.deltaY * 0.0015)))
    hoverLine()
    return
  }
  const f = Math.exp(e.deltaY * 0.0015)
  const nd = Math.max(0.5, Math.min(5000, cam.dist * f))
  // zoom toward the point under the cursor (not toward far geometry behind the target, e.g. the boundary walls)
  if (hoverPoint && f < 1 && camera.position.distanceTo(hoverPoint) < cam.dist * 1.5) cam.target.lerp(hoverPoint, 1 - nd / cam.dist)
  cam.dist = nd
  applyCamera()
}
function dbl() {
  if (hoverPoint) {
    if (camMode.value === 0) { cam.target.copy(hoverPoint); cam.dist = Math.min(cam.dist, 25) } else cam.pos.lerp(hoverPoint, 0.8)
    applyCamera()
  }
}
// Movement keys: WASD, Q / E and Space / Ctrl (world up / down), Shift ×4. Runs in the capture phase: while Ctrl is
// held for "down" over the view, Ctrl+W/A/S/D/Q/E must move the camera, not reach the app's shortcuts (Ctrl+W closes
// the tab), so those combinations are stopped here. Other Ctrl shortcuts (Ctrl+Tab, Ctrl+1…9, Ctrl+K) still work.
const MOVE = 'wasdqe'
const CAM_TITLES = [
  'Orbit: drag to rotate, right-drag / Shift-drag to pan, wheel to zoom, WASD moves, Space / Ctrl up / down, double-click to centre (C)',
  'Fly: drag to look, WASD to move, Space / E up, Ctrl / Q down, Shift ×4, wheel = speed (C)',
  'FPS noclip: click to capture the mouse, mouse looks, WASD along the view, Space / E up, Ctrl / Q down, Shift ×4, wheel = speed, Esc releases (N)',
]
const CAM_HELP = [
  'Orbit: drag to rotate · right-drag or Shift-drag to pan · wheel to zoom · WASD moves · Space / Ctrl up / down · double-click centres · click selects',
  'Fly: drag to look · WASD move · Space / E up · Ctrl / Q down · Shift ×4 · wheel = speed · click selects',
  'FPS: click to capture the mouse · mouse looks · WASD move · Space / E up · Ctrl / Q down · Shift ×4 · wheel = speed · click selects at the crosshair · Esc releases',
]
function keydown(e: KeyboardEvent) {
  const t = (e.target as HTMLElement)?.tagName
  if (t === 'INPUT' || t === 'TEXTAREA' || e.metaKey || e.altKey || ex.focus !== pane) return
  const k = e.key.toLowerCase()
  if (e.key === 'Escape' && (locked.value || performance.now() - lockReleasedAt < 400)) {
    unlock(); e.preventDefault(); e.stopImmediatePropagation(); return // releases the mouse only (not maximise)
  }
  if (e.key === 'Control') { if (mouse.in || locked.value || keys.size) keys.add('ctrl'); return }
  if (e.code === 'Space') { keys.add('space'); e.preventDefault(); return } // also keeps a focused button from clicking
  if (k.length === 1 && MOVE.includes(k)) {
    if (e.ctrlKey && !keys.has('ctrl')) return // Ctrl pressed outside the view: a normal shortcut
    keys.add(k)
    e.preventDefault()
    if (e.ctrlKey) e.stopImmediatePropagation()
    return
  }
  if (e.ctrlKey) return
  if (k === 'c') { setCamMode(camMode.value === 0 ? 1 : 0); return }
  if (k === 'n') { setCamMode(camMode.value === 2 ? prevMode : 2); return }
  if (k === 'r') { fitAll(); return }
  if (e.key === 'Delete') { hideSelection(e.shiftKey ? 'model' : 'selection'); return }
  if (e.key === 'Shift') keys.add('shift')
}
function keyup(e: KeyboardEvent) {
  keys.delete(e.key.toLowerCase())
  if (e.key === 'Shift') keys.delete('shift')
  if (e.key === 'Control') keys.delete('ctrl')
  if (e.code === 'Space') { keys.delete('space'); if (ex.focus === pane) e.preventDefault() }
}
function clearKeys() { keys.clear() }
function stepKeys(dt: number) {
  if (!keys.size || (keys.size === 1 && keys.has('shift'))) return
  const v = new THREE.Vector3()
  const fwd = back().negate(), right = new THREE.Vector3().setFromMatrixColumn(camera.matrixWorld, 0)
  if (camMode.value === 0) { fwd.y = 0; fwd.normalize() } // orbit: move the target over the ground
  if (keys.has('w')) v.add(fwd)
  if (keys.has('s')) v.sub(fwd)
  if (keys.has('d')) v.add(right)
  if (keys.has('a')) v.sub(right)
  if (keys.has('e') || keys.has('space')) v.y += 1
  if (keys.has('q') || keys.has('ctrl')) v.y -= 1
  if (!v.lengthSq()) return
  const speed = (camMode.value === 0 ? Math.max(4, cam.dist * 0.6) : cam.speed) * (keys.has('shift') ? 4 : 1)
  v.normalize().multiplyScalar(speed * dt)
  if (camMode.value === 0) cam.target.add(v); else cam.pos.add(v)
  applyCamera()
}

// ---- hover: the mesh point under the cursor (Go BVH ray), shapes (three), the placement under the cursor ----
const mouse = { x: 0, y: 0, in: false }
let hoverPoint: THREE.Vector3 | null = null // scene coordinates
let hoverHit: any = null
let hoverShape = ''
let hoverMarker: any = null
let busy = false, again = false
const ray = new THREE.Raycaster()
function ndc(cx: number, cy: number) {
  const r = cv.value!.getBoundingClientRect()
  return new THREE.Vector2(((cx - r.left) / r.width) * 2 - 1, -((cy - r.top) / r.height) * 2 + 1)
}
function hoverSoon() {
  if (busy) { again = true; return }
  busy = true
  hoverNow().finally(() => { busy = false; if (again) { again = false; hoverSoon() } })
}
async function hoverNow() {
  if (!cv.value || (!mouse.in && !locked.value) || !V) return
  if (locked.value) ray.setFromCamera(new THREE.Vector2(0, 0), camera) // the crosshair
  else ray.setFromCamera(ndc(mouse.x, mouse.y), camera)
  const o = ray.ray.origin, d = ray.ray.direction
  const m = p.mirror ? -1 : 1
  let h: any = null
  const bits = showCol.value ? (layers.solid ? PART_BIT.solid : 0) | (layers.other ? PART_BIT.other : 0) : 0 // not the translucent clip
  if (bits) {
    try { h = await api.MapRay(p.guid, [m * o.x, o.y, o.z], [m * d.x, d.y, d.z], bits) } catch { h = null }
  }
  hoverHit = h?.hit ? h : null
  hoverPoint = hoverHit ? toScene(hoverHit.pos) : null
  // shapes in front of the mesh hit
  hoverShape = ''
  const objs = showCol.value ? Object.values(shapeGroups).flat().filter((x) => x.visible && shapeRanges.get(x as THREE.Mesh)?.solid) : []
  const hits = objs.length ? ray.intersectObjects(objs, false) : []
  if (hits.length && (!hoverHit || hits[0]!.distance < hoverHit.t - 0.01)) {
    const it = hits[0]!, r = shapeRanges.get(it.object as THREE.Mesh)!
    const g = (it.object as THREE.Mesh).geometry
    const grp = g.groups.findIndex((q) => it.faceIndex! * 3 >= q.start && it.faceIndex! * 3 < q.start + q.count)
    const id = r.ids[grp] ?? -1
    const sh = shapes?.[r.kind]?.[id]
    hoverShape = `${r.kind.slice(0, -1)} #${id}` + (sh ? ` mask 0x${sh.mask.toString(16)}${heroBlocks(sh.mask) ? ' (blocks heroes)' : ''}` : '')
    hoverPoint = it.point.clone() // scene coordinates
    hoverHit = null
    hoverShapePos = it.point.clone()
  } else hoverShapePos = null
  const r = cv.value.getBoundingClientRect()
  hoverMarker = locked.value ? nearestMarker(r.left + r.width / 2, r.top + r.height / 2, 9) : nearestMarker(mouse.x, mouse.y, 9)
  hoverLine()
}
let hoverShapePos: THREE.Vector3 | null = null
function nearestMarker(cx: number, cy: number, px: number) {
  if (!cv.value) return null
  const r = cv.value.getBoundingClientRect()
  const mx = cx - r.left, my = cy - r.top
  const occl = hoverPoint ? camera.position.distanceTo(hoverPoint) + 1.0 : Infinity
  const v = new THREE.Vector3()
  let best: any = null, bd = px * px
  for (const x of markerList) {
    v.copy(toScene(x.pos))
    const dist = camera.position.distanceTo(v)
    v.project(camera)
    if (v.z > 1 || v.z < -1) continue
    const sx = (v.x + 1) / 2 * r.width, sy = (1 - v.y) / 2 * r.height
    let d = (sx - mx) ** 2 + (sy - my) ** 2
    if (d > px * px) continue
    if (dist > occl) d *= 3 // behind the surface under the cursor
    if (x.class === 'networked' || x.class === 'mover') d *= 0.5
    if (d < bd) { bd = d; best = x }
  }
  return best
}
const f2 = (n: number) => n.toFixed(2)
// a placement's label: its name, else its type and the GUID's short form (entity 167 = definition 0x0400…0167)
const markerName = (x: any) => x.name || p.typeNames[x.type] + (x.guid ? ' ' + (x.guid.slice(4).replace(/^0+/, '') || '0') : '')
function hoverLine() {
  let s = ''
  if (hoverHit) {
    const n = hoverHit.normal
    const kind = n[1] > 0.7 ? 'floor' : n[1] < -0.7 ? 'ceiling' : 'wall'
    s = `x ${f2(hoverHit.pos[0])}  y ${f2(hoverHit.pos[1])}  z ${f2(hoverHit.pos[2])}  ·  ${kind}, tri ${hoverHit.tri} mask 0x${hoverHit.mask.toString(16)} (${partOf(hoverHit.mask) === 'other' ? 'not heroes' : 'blocks heroes'}) flags 0x${hoverHit.flags.toString(16)} mat ${hoverHit.material}`
  } else if (hoverShape && hoverShapePos) {
    const q = hoverShapePos
    s = `x ${f2(p.mirror ? -q.x : q.x)}  y ${f2(q.y)}  z ${f2(q.z)}  ·  ${hoverShape}`
  } else s = mouse.in ? '(no collision under the cursor)' : ''
  if (hoverMarker) s += `   ·   ${markerName(hoverMarker)} at (${hoverMarker.pos.map(f2).join(', ')})`
  if (camMode.value === 1) s += `   ·   fly ${cam.speed.toFixed(1)} m/s`
  if (camMode.value === 2) {
    const c = camera.position
    s = `camera x ${f2(p.mirror ? -c.x : c.x)}  y ${f2(c.y)}  z ${f2(c.z)}   ·   noclip ${cam.speed.toFixed(1)} m/s` +
      (locked.value ? '' : !capture.value || lockFailed.value ? '   ·   drag to look' + (lockFailed.value ? ' (pointer lock unavailable)' : '') : '   ·   click the view to capture the mouse') +
      (s && (locked.value || mouse.in) ? `   ·   aim ${s}` : '')
  }
  ex.mapCursor = s
  reportAim()
}
// MCP current_view: camera and the point under the cursor / crosshair, in game coordinates (MapView owns extra.map)
const r2 = (n: number) => Math.round(n * 100) / 100
const game = (v: THREE.Vector3) => [r2(p.mirror ? -v.x : v.x), r2(v.y), r2(v.z)]
function reportCamera() {
  const m = ex.extra.map
  if (!m) return
  m.camera = { mode: ['orbit', 'fly', 'fps'][camMode.value] ?? 'orbit', pos: game(camera.position),
    target: camMode.value === 0 ? game(cam.target) : undefined, yawDeg: r2(cam.yaw * 180 / Math.PI), pitchDeg: r2(cam.pitch * 180 / Math.PI),
    distance: camMode.value === 0 ? r2(cam.dist) : undefined, speed: camMode.value ? r2(cam.speed) : undefined }
}
function reportAim() {
  const m = ex.extra.map
  if (!m) return
  const n = hoverHit?.normal
  m.aim = hoverHit ? { pos: hoverHit.pos.map(r2), surface: n[1] > 0.7 ? 'floor' : n[1] < -0.7 ? 'ceiling' : 'wall', tri: hoverHit.tri,
    mask: '0x' + hoverHit.mask.toString(16), flags: '0x' + hoverHit.flags.toString(16), material: hoverHit.material,
    blocksHeroes: partOf(hoverHit.mask) !== 'other', crosshair: camMode.value === 2 } : hoverShape ? { shape: hoverShape } : null
  m.hoverRecord = hoverMarker ? hoverMarker.i : undefined
}
function leave() { mouse.in = false; drag = null; if (!locked.value) { ex.mapCursor = ''; hoverMarker = null } }
// a click selects the model instance under the cursor / crosshair (T064), unless a marker near the cursor lies in
// front of it (markers of the same record give the instance too)
function choose(m: any, rc: THREE.Raycaster) {
  const h = pickModel(rc)
  if (h && (!m || m.i === h.record || camera.position.distanceTo(toScene(m.pos)) > h.dist + 0.5)) emit('select', h.record, h)
  else if (m) emit('select', m.i)
}
function pickCentre() {
  const r = cv.value!.getBoundingClientRect()
  const rc = new THREE.Raycaster()
  rc.setFromCamera(new THREE.Vector2(0, 0), camera)
  choose(nearestMarker(r.left + r.width / 2, r.top + r.height / 2, 14), rc)
}
function pick(e: MouseEvent) {
  const rc = new THREE.Raycaster()
  rc.setFromCamera(ndc(e.clientX, e.clientY), camera)
  choose(nearestMarker(e.clientX, e.clientY, 11), rc)
}

// ---- overlay: labels (networked + movers, the selection, the hovered marker), origin labels, corner gizmo ----
const labelEls = ref<{ key: string; x: number; y: number; text: string; cls: string; sel?: boolean }[]>([])
function project(pw: THREE.Vector3, r: DOMRect) {
  const v = pw.clone().project(camera)
  if (v.z > 1 || v.z < -1) return null
  return { x: (v.x + 1) / 2 * r.width, y: (1 - v.y) / 2 * r.height }
}
function updateLabels() {
  if (!cv.value) return
  const r = cv.value.getBoundingClientRect()
  const out: typeof labelEls.value = []
  const add = (x: any, key: string, sel = false) => {
    const q = project(toScene(x.pos), r)
    if (!q || q.x < -50 || q.y < -20 || q.x > r.width + 50 || q.y > r.height + 20) return
    out.push({ key, x: q.x, y: q.y, text: markerName(x), cls: p.cls(x.class).c, sel })
  }
  if (layers.labels) {
    for (const x of markerList) {
      if ((x.class !== 'networked' && x.class !== 'mover') || x.i === p.sel) continue
      if (camera.position.distanceTo(toScene(x.pos)) > 160) continue
      add(x, 'l' + x.i)
    }
  }
  const s = p.v?.placements?.[p.sel]
  if (s?.hasPos) add(s, 'sel', true)
  const o = project(new THREE.Vector3(0, 0, 0), r)
  if (o) out.push({ key: 'origin', x: o.x, y: o.y, text: '0, 0, 0', cls: '#d8e0ec' })
  for (const [ax, c, t] of [[[5.6, 0, 0], '#ff6b6b', 'X'], [[0, 5.6, 0], '#7fd68a', 'Y'], [[0, 0, 5.6], '#6ba6ff', 'Z']] as const) {
    const q = project(toScene(ax as any), r)
    if (q) out.push({ key: 'ax' + t, x: q.x, y: q.y, text: t, cls: c })
  }
  labelEls.value = out
}
const axes = new THREE.AxesHelper(5) // at the world origin, in game coordinates (mirrors with the world)
axes.renderOrder = 5;
(axes.material as THREE.Material).depthTest = false
world.add(axes)
function drawGizmo() {
  const c = gizmo.value
  if (!c) return
  const dpr = window.devicePixelRatio || 1, S = 76
  c.width = S * dpr; c.height = S * dpr
  const g = c.getContext('2d')!
  g.setTransform(dpr, 0, 0, dpr, 0, 0)
  g.clearRect(0, 0, S, S)
  g.fillStyle = 'rgba(21,27,40,0.85)'
  g.beginPath(); g.arc(S / 2, S / 2, S / 2 - 1, 0, Math.PI * 2); g.fill()
  const inv = camera.matrixWorldInverse
  const ax = [[p.mirror ? -1 : 1, 0, 0, '#ff6b6b', 'X'], [0, 1, 0, '#7fd68a', 'Y'], [0, 0, 1, '#6ba6ff', 'Z']] as const
  const items = ax.map(([x, y, z, col, t]) => ({ v: new THREE.Vector3(x, y, z).transformDirection(inv), col, t }))
  items.sort((a, b) => a.v.z - b.v.z)
  g.font = '10px JetBrains Mono Variable, monospace'
  for (const it of items) {
    const ex2 = S / 2 + it.v.x * 26, ey = S / 2 - it.v.y * 26
    g.strokeStyle = it.col; g.fillStyle = it.col; g.lineWidth = 2
    g.globalAlpha = it.v.z < -0.2 ? 1 : 0.55
    g.beginPath(); g.moveTo(S / 2, S / 2); g.lineTo(ex2, ey); g.stroke()
    g.beginPath(); g.arc(ex2, ey, 6, 0, Math.PI * 2); g.fill()
    g.fillStyle = '#0e131e'; g.fillText(it.t, ex2 - 3, ey + 3.5)
  }
  g.globalAlpha = 1
}

// ---- loop ----
let raf = 0, last = performance.now(), lastCentre = 0
function frame(now: number) {
  raf = requestAnimationFrame(frame)
  const dt = Math.min(0.1, (now - last) / 1000)
  last = now
  stepKeys(dt)
  if (!dirty || !renderer) return
  dirty = false
  renderScene()
  updateLabels()
  drawGizmo()
  if (now - lastCentre > 250) { lastCentre = now; centreRay() }
}
async function centreRay() {
  if (!V) return
  const o = camera.position, d = camera.getWorldDirection(new THREE.Vector3())
  const m = p.mirror ? -1 : 1
  try {
    const h = await api.MapRay(p.guid, [m * o.x, o.y, o.z], [m * d.x, d.y, d.z], PART_BIT.solid | PART_BIT.other)
    centreHit = h?.hit ? h.t : null
  } catch { centreHit = null }
}
function resize() {
  const w = wrap.value
  if (!w || !renderer) return
  const r = w.getBoundingClientRect()
  renderer.setSize(r.width, r.height, false)
  if (composer) { composer.setPixelRatio(renderer.getPixelRatio()); composer.setSize(r.width, r.height) }
  camera.aspect = r.width / Math.max(1, r.height)
  camera.updateProjectionMatrix()
  redraw()
}
watch(() => p.mirror, (m, old) => {
  world.scale.x = m ? -1 : 1
  world.updateMatrixWorld()
  placeSun()
  if (probes) (probes.uniforms.owGameFromWorld.value as THREE.Matrix4).copy(world.matrixWorld).invert()
  if (old !== undefined) { // keep looking at the same game point
    cam.target.x = -cam.target.x; cam.pos.x = -cam.pos.x; cam.yaw = -cam.yaw
  }
  applyCamera()
}, { immediate: true })

let ro: ResizeObserver | null = null
onMounted(async () => {
  try {
    renderer = new THREE.WebGLRenderer({ canvas: cv.value!, antialias: true })
    renderer.setPixelRatio(window.devicePixelRatio || 1)
  } catch (e: any) {
    err.value = 'WebGL is not available in this webview: ' + String(e?.message ?? e)
    return
  }
  ro = new ResizeObserver(resize)
  ro.observe(wrap.value!)
  resize()
  window.addEventListener('keydown', keydown, true)
  window.addEventListener('keyup', keyup)
  window.addEventListener('blur', clearKeys)
  document.addEventListener('pointerlockchange', lockChange)
  document.addEventListener('pointerlockerror', lockError)
  raf = requestAnimationFrame(frame)
  try {
    const t0 = performance.now()
    const [sh, ms] = await Promise.all([api.MapShapes(p.guid), loadMesh()])
    shapes = sh
    bounds = { min: sh.min, max: sh.max }
    info.hulls = sh.hulls.length; info.capsules = sh.capsules.length; info.spheres = sh.spheres.length
    buildShapes(sh)
    buildMarkers()
    paint() // height colours need the bounds
    fitAll()
    initMapLighting() // needs the bounds (the shadow map covers the playable area)
    if (p.sel >= 0) focus(p.sel) // a selection from the 2D plot or a deep link ow://<map>/map/<record>
    status.value = ''
    info.ms = Math.round(performance.now() - t0)
    // one full frame, finished (first draw uploads the buffers)
    const tf = performance.now(); renderer.render(scene, camera); renderer.getContext().finish()
    const tf2 = performance.now(); renderer.render(scene, camera); renderer.getContext().finish()
    LogPrint(`map3d ${p.guid}: ${info.tris} triangles (${info.solid} solid, ${info.clip} clip, ${info.other} other), ${info.hulls} hulls, ${info.capsules} capsules, ${info.spheres} spheres; mesh ${ms.toFixed(0)} ms, total ${info.ms} ms; first frame ${(tf2 - tf).toFixed(0)} ms, next ${(performance.now() - tf2).toFixed(0)} ms`)
  } catch (e: any) {
    err.value = String(e?.message ?? e)
    LogPrint(`map3d ${p.guid}: error ${err.value}`)
  }
})
onBeforeUnmount(() => {
  unmounted = true
  resetPBR()
  envTex?.dispose()
  disposeMapEnvironment(menv)
  probes?.dispose()
  composer?.dispose()
  cancelAnimationFrame(raf)
  ro?.disconnect()
  window.removeEventListener('keydown', keydown, true)
  window.removeEventListener('keyup', keyup)
  window.removeEventListener('blur', clearKeys)
  document.removeEventListener('pointerlockchange', lockChange)
  document.removeEventListener('pointerlockerror', lockError)
  unlock()
  ex.mapCursor = ''
  scene.traverse((o: any) => { o.geometry?.dispose?.(); if (o.material) [o.material].flat().forEach((m: any) => m.dispose?.()) })
  renderer?.dispose()
  renderer = null
})
</script>

<template>
  <div ref="wrap" class="absolute inset-0 select-none" :class="camMode === 1 ? 'cursor-move' : 'cursor-crosshair'"
    @mousedown="down" @mousemove="move" @mouseup="up" @mouseleave="leave" @wheel.prevent="wheel" @dblclick="dbl" @contextmenu.prevent>
    <canvas ref="cv" class="absolute inset-0 h-full w-full" />
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <span v-for="l in labelEls" :key="l.key" class="absolute -translate-x-1/2 translate-y-2 whitespace-nowrap rounded px-1 text-[11px] leading-tight"
        :class="l.sel ? 'bg-panel/95 font-medium text-fg ring-1 ring-white/60' : 'bg-panel/70'" :style="{ left: l.x + 'px', top: l.y + 'px', color: l.sel ? undefined : l.cls }">{{ l.text }}</span>
    </div>
    <div v-if="camMode === 2 && locked" class="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 text-[18px] leading-none text-white/80">+</div>
    <canvas ref="gizmo" class="pointer-events-none absolute right-3 bottom-8" style="width: 76px; height: 76px" />
    <button class="mono absolute right-3 bottom-[118px] rounded bg-panel/85 px-2 py-1 text-right text-[11px] leading-tight text-dim hover:text-fg" title="The camera's position in game coordinates (x, y, z), yaw / pitch. Click: copy x, y, z" @mousedown.stop @mouseup.stop @dblclick.stop @click.stop="copyCam">
      <div>cam <span class="text-fg">{{ camInfo.pos.map((x) => x.toFixed(2)).join('  ') }}</span></div>
      <div>yaw {{ camInfo.yaw.toFixed(1) }}° · pitch {{ camInfo.pitch.toFixed(1) }}°<template v-if="camMode !== 0"> · {{ cam.speed.toFixed(1) }} m/s</template></div>
    </button>
    <div class="absolute top-2 right-2 w-56 rounded bg-panel/90 px-2 py-1.5 text-[11px]" @mousedown.stop @mouseup.stop @wheel.stop @dblclick.stop>
      <div class="mb-1 flex gap-1">
        <button v-for="(m, k) in ['Orbit', 'Fly', 'FPS']" :key="m" class="rounded px-2 py-0.5 hover:bg-raised" :class="camMode === k ? 'bg-sel text-fg' : 'text-dim'" :title="CAM_TITLES[k]" @click="setCamMode(k)">{{ m }}</button>
        <button class="ml-auto rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" title="Reset the camera (R)" @click="fitAll">Reset</button>
        <button class="rounded px-1.5 py-0.5 text-dim hover:bg-raised hover:text-fg" :title="panelOpen ? 'Hide the 3D layers' : 'Show the 3D layers'" @click="togglePanel">{{ panelOpen ? '▴' : '▾' }}</button>
      </div>
      <label v-if="camMode === 2" class="mt-1 flex cursor-pointer items-center gap-2" title="Capture the mouse (Pointer Lock) so it looks around without a button; Esc releases it. Off: drag to look."><input type="checkbox" class="accent-[#8fb4ff]" :checked="capture" @change="setCapture(($event.target as HTMLInputElement).checked)" />capture mouse (pointer lock)</label>
      <div v-if="camMode === 2" class="mt-1 flex items-center gap-2" title="Mouse look sensitivity (FPS, also drag to look), 0.1×–10×; double-click resets to 3×">
        <span class="text-dim">sensitivity</span>
        <input type="range" min="-1" max="1" step="0.01" class="min-w-0 flex-1 accent-[#8fb4ff]" :value="sensLog" @input="setSens(Number(($event.target as HTMLInputElement).value))" @change="saveSens" @dblclick="resetSens" />
        <span class="mono w-10 text-right">{{ sens < 1 ? sens.toFixed(2) : sens.toFixed(1) }}×</span>
      </div>
      <div class="mt-1 flex gap-1" title="Which geometry is drawn: the placed models with their textures (visual), the collision mesh and shapes, or both (remembered)">
        <button class="flex-1 rounded px-2 py-0.5 hover:bg-raised" :class="showModels ? 'bg-sel text-fg' : 'text-dim'" @click="toggleGeo(2)">models (visual)</button>
        <button class="flex-1 rounded px-2 py-0.5 hover:bg-raised" :class="showCol ? 'bg-sel text-fg' : 'text-dim'" @click="toggleGeo(1)">collision</button>
        <button class="flex-1 rounded px-2 py-0.5 hover:bg-raised" :class="showHelpers ? 'bg-sel text-fg' : 'text-dim'" title="Helper / editor geometry: submeshes whose material has +0x68 bit 1 (&quot;Player Only / No Walk / No Grapple&quot; boxes, the checker placeholder); not drawn in game [?]" @click="toggleGeo(4)">helpers</button>
      </div>
      <div v-if="showModels || showHelpers" class="mt-1 flex items-center gap-1" title="Texture size of the models: the largest mip up to this size. Starts at 256 px on every open; larger sizes load slower and use ~4× GPU memory per step (Anubis ~65 MB at 256, ~1 GB at 1024).">
        <span class="text-dim">textures</span>
        <button v-for="n in TEX_SIZES" :key="n" class="rounded px-1.5 py-0.5 hover:bg-raised" :class="texSize === n ? 'bg-sel text-fg' : 'text-dim'" @click="setTexSize(n)">{{ n === 4096 ? 'full' : n }}</button>
      </div>
      <div v-if="showModels || showHelpers" class="mt-1 flex items-center gap-1" title="simple: the colour texture, Lambert (T064). material: each game material's PBR inputs (the Material tab's mapping), one shared material per game material, maps at min(texture size, 512) px, built nearest first; lit by a studio environment + the sun, not the game's probes / lightmaps / GI. Off on every open.">
        <span class="text-dim">shading</span>
        <button class="rounded px-1.5 py-0.5 hover:bg-raised" :class="!pbrMode ? 'bg-sel text-fg' : 'text-dim'" @click="setPBRMode(0)">simple</button>
        <button class="rounded px-1.5 py-0.5 hover:bg-raised" :class="pbrMode ? 'bg-sel text-fg' : 'text-dim'" @click="setPBRMode(1)">material</button>
        <span v-if="pbrMode" class="ml-auto text-faint">{{ pbr.materials }} mat<template v-if="pbr.converting"> · {{ pbr.converting }} left</template><template v-if="pbr.pending"> · {{ pbr.pending }} tex</template></span>
      </div>
      <div class="mt-1 flex items-center gap-1" title="studio: a neutral light (T064 / T071). map: the map's own sun (direction, HDR colour) with shadows, its sky as background and ambient light, ACES tone mapping and its colour-grading LUT (from the map data's environment block). Not the game's baked GI / lightmaps / probes / AO. Remembered.">
        <span class="text-dim">lighting</span>
        <button class="rounded px-1.5 py-0.5 hover:bg-raised" :class="!lightMode ? 'bg-sel text-fg' : 'text-dim'" @click="setLight('light', 0)">studio</button>
        <button class="rounded px-1.5 py-0.5 hover:bg-raised" :class="lightMode ? 'bg-sel text-fg' : 'text-dim'" @click="setLight('light', 1)">map</button>
        <span v-if="lightMode && envState.state && envState.state !== 'ok'" class="ml-auto truncate text-faint" :title="envState.state">{{ envState.state }}</span>
      </div>
      <div v-if="lightMode" class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="flex items-center gap-0.5 text-dim" title="Tone mapping (HDR → screen)">tone
          <button v-for="(t, i) in ['ACES', 'neutral', 'AgX']" :key="t" class="rounded px-1 hover:bg-raised" :class="toneMode === i ? 'bg-sel text-fg' : ''" @click="setLight('tone', i)">{{ t }}</button></span>
        <label class="flex items-center gap-1 text-dim" title="Sun shadows: one shadow map over the playable area, re-rendered only when models load or change. Off on every open"><input type="checkbox" :checked="!!shadowsOn" @change="setLight('shadows', shadowsOn ? 0 : 1)">shadows</label>
        <label class="flex items-center gap-1 text-dim" :title="`The map's ${envState.probes} reflection probes as reflections (box-projected) and as ambient light (their blurriest level), blended at box edges; the map-wide capture outside them. Material shading only`"><input type="checkbox" :checked="!!reflOn" :disabled="envState.probes <= 0" @change="setLight('refl', reflOn ? 0 : 1)">reflections</label>
        <label v-if="reflOn && envState.probes > 0" class="flex items-center gap-1 text-dim" title="Mirror test: every surface becomes a perfect mirror, to see what the probes reflect (material shading; not remembered)"><input v-model="mirrorTest" type="checkbox">mirror</label>
        <label class="flex items-center gap-1 text-dim" :title="envState.lut ? 'The map\'s colour-grading LUT (32³)' : 'This map has no LUT'"><input type="checkbox" :checked="!!gradeOn" :disabled="!envState.lut" @change="setLight('grade', gradeOn ? 0 : 1)">grading</label>
        <span class="flex flex-1 items-center gap-1 text-dim" title="Tone-mapping exposure (double-click: 0.40)">exp
          <input type="range" min="20" max="250" step="5" class="min-w-0 flex-1 accent-[#8fb4ff]" :value="exposure" @input="slideExposure(Number(($event.target as HTMLInputElement).value))" @change="slideExposure(Number(($event.target as HTMLInputElement).value), true)" @dblclick="slideExposure(40, true)" />
          <span class="mono w-8 text-right">{{ (exposure / 100).toFixed(2) }}</span></span>
      </div>
      <div v-if="showModels || showHelpers" class="mt-1 flex items-center gap-1" title="Render frames back to back for 3 s, each waiting for the GPU (not paced by the display), and show the time per frame and the rate it allows, capped at 160 fps ('+' = at the cap). The view as it is now.">
        <button class="rounded px-1.5 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="measureFrames()">measure frame time</button>
        <span v-if="bench.running" class="text-faint">measuring 3 s…</span>
        <span v-else-if="bench.frames" class="mono text-faint">{{ bench.ms }} ms · {{ bench.fps }}{{ bench.capped ? '+' : '' }} fps ({{ bench.mode }})</span>
      </div>
      <div v-if="hiddenList.length" class="mt-1 rounded border border-line/60 px-1.5 py-1">
        <div class="flex items-center"><span class="text-dim">Hidden ({{ hiddenList.length }})</span><button class="ml-auto text-dim hover:text-fg" @click="unhideAll">unhide all</button></div>
        <div class="max-h-40 overflow-auto">
          <div v-for="h in hiddenList" :key="h.key" class="flex items-center gap-1 py-px">
            <span class="min-w-0 flex-1 truncate" :title="h.label">{{ h.label }}</span>
            <button class="shrink-0 rounded px-1 text-dim hover:bg-raised hover:text-fg" title="Unhide" @click="unhide(h.key)">show</button>
          </div>
        </div>
      </div>
      <p v-if="(showModels || showHelpers) && vinfo.batches" class="text-faint">{{ vinfo.state === 'loading' ? `loading ${vinfo.done} / ${vinfo.batches} · ` : '' }}{{ vinfo.batches }} models × looks, {{ num(vinfo.instances) }} placed ({{ num(vinfo.helpers) }} with helper parts), {{ num(vinfo.tris) }} △<template v-if="vinfo.failed"> · {{ vinfo.failed }} failed</template></p>
      <template v-if="panelOpen">
      <p class="mt-1 text-faint">Collision mesh <span class="mono">{{ num(info.tris) }}</span> triangles<template v-if="info.ms"> · loaded in {{ (info.ms / 1000).toFixed(1) }} s</template></p>
      <label class="flex cursor-pointer items-center gap-2 py-px" title="Triangles whose mask has a hero category (0x08 / 0x10) and others"><input v-model="layers.solid" type="checkbox" class="accent-[#8fb4ff]" />solid (blocks heroes) <span class="mono ml-auto text-faint">{{ num(info.solid) }}</span></label>
      <label class="flex cursor-pointer items-center gap-2 py-px" title="Mask ⊆ 0x001A: only heroes collide (invisible walls and roofs). Translucent; the cursor goes through."><input v-model="layers.clip" type="checkbox" class="accent-[#8fb4ff]" /><span class="inline-block h-2 w-2 rounded-sm bg-[#e06a9a]" />heroes only (clip) <span class="mono ml-auto text-faint">{{ num(info.clip) }}</span></label>
      <label class="flex cursor-pointer items-center gap-2 py-px" title="Triangles whose mask has neither hero category (0x08 / 0x10)"><input v-model="layers.other" type="checkbox" class="accent-[#8fb4ff]" />not heroes <span class="mono ml-auto text-faint">{{ num(info.other) }}</span></label>
      <label class="flex cursor-pointer items-center gap-2 py-px"><input v-model="layers.hulls" type="checkbox" class="accent-[#8fb4ff]" /><span class="inline-block h-2 w-2 rounded-sm bg-[#e0955a]" />convex hulls <span class="mono ml-auto text-faint">{{ info.hulls }}</span></label>
      <label class="flex cursor-pointer items-center gap-2 py-px"><input v-model="layers.capsules" type="checkbox" class="accent-[#8fb4ff]" /><span class="inline-block h-2 w-2 rounded-sm bg-[#b58cf0]" />capsules <span class="mono ml-auto text-faint">{{ info.capsules }}</span></label>
      <label class="flex cursor-pointer items-center gap-2 py-px"><input v-model="layers.spheres" type="checkbox" class="accent-[#8fb4ff]" /><span class="inline-block h-2 w-2 rounded-sm bg-[#7fd68a]" />spheres <span class="mono ml-auto text-faint">{{ info.spheres }}</span></label>
      <p class="text-faint">Translucent shapes: masks without the hero categories (the cursor goes through).</p>
      <div class="mt-1 flex flex-wrap items-center gap-1">
        <span class="text-dim">Colour</span>
        <button v-for="(m, k) in COLOR_MODES" :key="m" class="rounded px-1.5 py-px hover:bg-raised" :class="colorMode === k ? 'bg-sel text-fg' : 'text-dim'" @click="setColor(k)">{{ m }}</button>
      </div>
      <div v-if="colorMode === 0" class="mt-1 grid grid-cols-2 gap-x-2 text-faint">
        <span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#c2ad86]" />floor</span>
        <span title="Upward-facing, surface flag bit 0 clear"><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#c28470]" />floor, flag 0 off</span>
        <span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#8693a9]" />wall</span>
        <span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#626b80]" />ceiling</span>
        <span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#4f9c94]" /><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#3e727d]" />not heroes</span>
        <span><span class="mr-1 inline-block h-2 w-2 rounded-sm bg-[#e06a9a]" />clip</span>
      </div>
      <div v-else-if="colorMode === 1" class="mt-1 h-2 rounded" style="background: linear-gradient(90deg, #2c3e8c, #21908d, #5ec962, #e0d04a, #f27a3e)" />
      <div v-else class="mt-1 max-h-40 overflow-auto">
        <div v-for="r in legendRows" :key="r.k" class="flex items-center gap-2"><span class="inline-block h-2 w-2 rounded-sm" :style="{ background: r.c }" /><span class="mono">{{ r.k }}</span><span v-if="r.hero" class="text-faint">heroes</span><span class="mono ml-auto text-faint">{{ num(r.n) }}</span></div>
      </div>
      <div class="mt-1 flex flex-wrap gap-x-3">
        <label class="flex cursor-pointer items-center gap-1"><input v-model="layers.labels" type="checkbox" class="accent-[#8fb4ff]" />names</label>
        <label class="flex cursor-pointer items-center gap-1" title="Markers behind geometry, faint"><input v-model="layers.ghosts" type="checkbox" class="accent-[#8fb4ff]" />hidden markers</label>
        <label class="flex cursor-pointer items-center gap-1"><input v-model="layers.wire" type="checkbox" class="accent-[#8fb4ff]" />wireframe</label>
      </div>
      </template>
    </div>
    <p v-if="status || err" class="absolute inset-x-0 top-1/2 text-center" :class="err ? 'text-v-err' : 'text-dim'">{{ err || status }}</p>
    <p class="pointer-events-none absolute right-24 bottom-7 left-3 truncate text-right text-[11px] text-faint">
      {{ CAM_HELP[camMode] }} · C orbit / fly · N FPS · R reset{{ mirror ? ' · mirrored (x)' : '' }}
    </p>
  </div>
</template>
