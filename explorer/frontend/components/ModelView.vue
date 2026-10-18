<script setup lang="ts">
// Model (0x00C) or model look (0x01A → the first model placed with it): a 3D preview of one LOD with the look's
// colour textures, the submeshes, material slots, vertex layouts and skeleton, glTF / OBJ export
// (docs/Explorer.md "Model view"). Decoding happens in Go (internal/model); the mesh comes over the asset server
// (/model/<guid>.bin), the textures as /mtex/<guid>.png.
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'
import { bytes, num } from '~/utils/format'
import { fetchMesh, materialTexture, type MeshGroup } from '~/utils/owmd'
import { MaterialBuilder, patchLayers, studioEnvironment, type Preview } from '~/utils/owmat'

const p = defineProps<{ guid: string }>()
const { s: ex, pane } = useExplorer()
const v = ref<any>(null)
const err = ref('')
const note = ref('')
const loading = ref(false)
const sideW = usePanel('model.side', 360)
const lod = ref(0)                 // index into v.lods
const look = ref('')                // chosen look ('' = the backend's choice)
const shade = ref<'texture' | 'material' | 'normal' | 'plain'>('texture')
const wire = ref(false)
const bones = ref(false)
const mirror = ref(false)
const proxy = ref(false)             // also draw the flag-0x10 proxy submeshes
const sel = ref(-1)                 // selected submesh
// texture shading: "simple" = the colour texture with Lambert (T064); "material (PBR)" = each slot material's inputs
// through the shared builder (utils/owmat.ts, T071) with studio lighting (environment + sun). Remembered.
const pbr = (() => { const r = ref(0) as any; r.commit = () => {}; return r })() // off on every open (user, 2026-09-29)
function setPBR(on: number) { if (pbr.value === on) return; pbr.value = on; pbr.commit() }
const previews = shallowRef<Record<string, Preview>>({})
async function loadPreviews() {
  const mats = [...new Set((v.value?.slots ?? []).map((s: any) => s.material).filter((m: string) => m && !previews.value[m]))]
  if (!mats.length) return
  try { previews.value = { ...previews.value, ...(await api.MaterialPreviews(mats)) } } catch { /* the slot falls back to simple */ }
}
const stats = reactive({ verts: 0, tris: 0, groups: 0 })

async function fetchView(lk = '') {
  try {
    loading.value = true
    v.value = await api.Model(p.guid, lk)
    look.value = v.value.look
    err.value = ''
  } catch (e: any) { err.value = String(e) } finally { loading.value = false }
}
onMounted(async () => {
  await fetchView()
  if (!v.value) return
  if (ex.instance >= 0 && ex.instance < v.value.submeshes.length) selectSubmesh(ex.instance)
  await nextTick()
  init()
  loadMesh()
})

// ---- three.js scene ----
const wrap = ref<HTMLDivElement>()
let renderer: THREE.WebGLRenderer | null = null
let scene: THREE.Scene, camera: THREE.PerspectiveCamera, controls: OrbitControls
let hemi: THREE.HemisphereLight, fillLight: THREE.DirectionalLight, sunLight: THREE.DirectionalLight, env: THREE.Texture | null = null
const builder = new MaterialBuilder(() => { dirty = true })
let root: THREE.Group, skel: THREE.LineSegments | null = null
const meshes = shallowRef<THREE.Mesh[]>([])
let geo: THREE.BufferGeometry | null = null
let groups: MeshGroup[] = []
let raf = 0
let dirty = true
const texCache = new Map<string, THREE.Texture>()

function init() {
  const el = wrap.value!
  renderer = new THREE.WebGLRenderer({ antialias: true })
  renderer.setPixelRatio(window.devicePixelRatio)
  renderer.outputColorSpace = THREE.SRGBColorSpace
  el.appendChild(renderer.domElement)
  scene = new THREE.Scene()
  scene.background = new THREE.Color(0x10151f)
  hemi = new THREE.HemisphereLight(0xdfe8ff, 0x3a3228, 1.6)
  scene.add(hemi)
  env = studioEnvironment(renderer)
  const sun = new THREE.DirectionalLight(0xffffff, 1.6)
  sun.position.set(3, 5, 4)
  scene.add(sun)
  sunLight = sun
  camera = new THREE.PerspectiveCamera(40, 1, 0.01, 5000)
  scene.add(camera)
  const fill = new THREE.DirectionalLight(0xffffff, 0.5)
  fill.position.set(-2, 1, 3)
  camera.add(fill)
  fillLight = fill
  applyLighting()
  root = new THREE.Group()
  scene.add(root)
  scene.add(new THREE.GridHelper(10, 20, 0x33405a, 0x222b3c))
  controls = new OrbitControls(camera, renderer.domElement)
  controls.enableDamping = false
  controls.addEventListener('change', () => { dirty = true; report() })
  new ResizeObserver(resize).observe(el)
  renderer.domElement.addEventListener('pointerdown', pdown)
  renderer.domElement.addEventListener('pointerup', pup)
  resize()
  const loop = () => { raf = requestAnimationFrame(loop); if (dirty && renderer) { dirty = false; renderer.render(scene, camera) } }
  loop()
}
function resize() {
  if (!renderer || !wrap.value) return
  const w = wrap.value.clientWidth, h = wrap.value.clientHeight
  renderer.setSize(w, h)
  camera.aspect = w / Math.max(1, h)
  camera.updateProjectionMatrix()
  dirty = true
}
onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  geo?.dispose()
  texCache.forEach((t) => t.dispose())
  builder.dispose()
  env?.dispose()
  renderer?.dispose()
  renderer?.domElement.remove()
  renderer = null
  delete ex.extra.model
  if (ex.extra.ask?.tab === 'model') delete ex.extra.ask
})

// slot colour for the "material" mode and for slots without a texture
const slotColor = (i: number) => new THREE.Color().setHSL(((i * 0.618) % 1), 0.45, 0.55)
function texture(guid: string) { return materialTexture(texCache, guid, 1024, () => { dirty = true }) }
const hiddenMat = new THREE.MeshBasicMaterial({ visible: false })
// PBR mode: studio lighting = the generated environment + the sun (no hemisphere / fill light), ACES tone mapping
const pbrOn = computed(() => shade.value === 'texture' && !!pbr.value)
function applyLighting() {
  if (!renderer) return
  scene.environment = pbrOn.value ? env : null
  hemi.visible = fillLight.visible = !pbrOn.value
  sunLight.intensity = pbrOn.value ? 1.1 : 1.6
  scene.environmentIntensity = 0.6
  renderer.toneMapping = pbrOn.value ? THREE.ACESFilmicToneMapping : THREE.NoToneMapping
  dirty = true
}
function materialFor(gr: { material: number; submesh: number }) {
  const slot = v.value?.slots?.[gr.material]
  const hl = sel.value === gr.submesh
  let m: THREE.Material
  const pv = pbrOn.value && slot?.material ? previews.value[slot.material] : undefined
  if (pv) {
    const b = builder.build(slot.material, pv, { size: 1024, context: 'model', physical: true, uv1: true })
    if (!b) return hiddenMat
    const s = b as any
    if (wire.value) s.wireframe = true
    if (hl && s.emissive && !s.emissiveMap) s.emissive = new THREE.Color(0x2a4a8a)
    return b
  }
  if (shade.value === 'normal') m = new THREE.MeshNormalMaterial({ side: THREE.DoubleSide, wireframe: wire.value })
  else {
    const opts: any = { side: THREE.DoubleSide, wireframe: wire.value, color: 0xb8b8b8 }
    if (shade.value === 'material') opts.color = slotColor(gr.material)
    else if (shade.value === 'texture' && slot?.decal) return hiddenMat // opacity without colour (a normal-only decal)
    else if (shade.value === 'texture' && slot?.color) { opts.map = texture(slot.color); opts.color = 0xffffff }
    if (shade.value === 'texture' && slot?.opacity) { opts.alphaMap = texture(slot.opacity); opts.transparent = true; opts.depthWrite = false }
    else if (shade.value === 'texture' && !slot?.material) opts.color = 0x8a6a6a // no entry in the look
    if (hl) opts.emissive = new THREE.Color(0x2a4a8a)
    m = new THREE.MeshLambertMaterial(opts)
    // layered materials (0x25 / 0x24): blend layer B / the overlay in the shader (T071 round 4)
    if (shade.value === 'texture' && slot?.material) patchLayers(m, slot.material, previews.value[slot.material], builder, 1024)
  }
  return m
}

async function loadMesh() {
  if (!v.value || !renderer) return
  const mask = v.value.lods[lod.value]?.mask ?? 255
  const seq = ++loadSeq
  loading.value = true
  try {
    const mesh = await fetchMesh(v.value.guid, mask, proxy.value)
    if (seq !== loadSeq) { mesh.geo.dispose(); return }
    geo?.dispose()
    geo = mesh.geo
    groups = mesh.groups
    stats.verts = mesh.verts; stats.tris = mesh.tris; stats.groups = groups.length
    build()
    frame()
  } catch (e: any) { err.value = String(e) } finally { if (seq === loadSeq) loading.value = false }
}
let loadSeq = 0

function build() {
  root.clear()
  meshes.value = []
  if (!geo) return
  const m = new THREE.Mesh(geo, groups.map(materialFor))
  meshes.value = [m]
  root.add(m)
  root.scale.x = mirror.value ? -1 : 1
  buildSkeleton()
  dirty = true
}
function restyle() {
  for (const m of meshes.value) {
    (m.material as THREE.Material[]).forEach((x) => { if (x !== hiddenMat) x.dispose() })
    m.material = groups.map(materialFor)
  }
  dirty = true
}
function buildSkeleton() {
  if (skel) { root.remove(skel); skel.geometry.dispose(); skel = null }
  const b = v.value?.skeleton?.bones
  if (!bones.value || !b) return
  const pts: number[] = []
  b.forEach((x: any) => { if (x.parent >= 0) pts.push(...b[x.parent].pos, ...x.pos) })
  const g = new THREE.BufferGeometry()
  g.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3))
  skel = new THREE.LineSegments(g, new THREE.LineBasicMaterial({ color: 0xffc34d, depthTest: false }))
  skel.renderOrder = 10
  root.add(skel)
}
function frame() {
  if (!geo?.boundingSphere) return
  const s = geo.boundingSphere, c = s.center.clone()
  if (mirror.value) c.x = -c.x
  const r = Math.max(s.radius, 0.05)
  controls.target.copy(c)
  camera.position.copy(c).add(new THREE.Vector3(0.9, 0.55, 1.4).normalize().multiplyScalar(r * 2.6))
  camera.near = r / 200; camera.far = r * 200
  camera.updateProjectionMatrix()
  controls.update()
  dirty = true
}

watch([lod, proxy], () => loadMesh())
watch([shade, wire, sel, previews], restyle)
watch([pbrOn, v], async () => { applyLighting(); await loadPreviews(); restyle() }) // previews: both modes (layer patch)
watch(bones, () => { buildSkeleton(); dirty = true })
watch(mirror, () => { root.scale.x = mirror.value ? -1 : 1; frame() })
async function chooseLook(g: string) {
  await fetchView(g)
  restyle()
}

// click on the model selects the submesh under the cursor
let pd: { x: number; y: number } | null = null
function pdown(e: PointerEvent) { pd = { x: e.clientX, y: e.clientY } }
function pup(e: PointerEvent) {
  if (!pd || Math.abs(e.clientX - pd.x) + Math.abs(e.clientY - pd.y) > 4 || e.button !== 0) { pd = null; return }
  pd = null
  const r = renderer!.domElement.getBoundingClientRect()
  const ray = new THREE.Raycaster()
  ray.setFromCamera(new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1), camera)
  const hit = ray.intersectObjects(meshes.value, false)[0]
  const k = hit?.face?.materialIndex
  if (k !== undefined && groups[k]) selectSubmesh(groups[k].submesh)
  else sel.value = -1
}
function selectSubmesh(i: number) { sel.value = sel.value === i ? -1 : i }

// MCP current_view + Ask
function report() {
  if (!v.value) return
  const c = camera?.position, t = controls?.target
  ex.extra.model = {
    model: v.value.guid, look: look.value, lod: v.value.lods[lod.value]?.mask, submesh: sel.value, shading: shade.value,
    wireframe: wire.value, skeleton: bones.value, mirrored: mirror.value, pbr: pbrOn.value, drawn: { ...stats },
    camera: c && t ? { position: [+c.x.toFixed(3), +c.y.toFixed(3), +c.z.toFixed(3)], target: [+t.x.toFixed(3), +t.y.toFixed(3), +t.z.toFixed(3)] } : undefined,
  }
}
watch([v, lod, sel, shade, wire, bones, mirror, () => stats.tris], () => {
  report()
  if (!v.value) return
  const sm = sel.value >= 0 ? v.value.submeshes[sel.value] : null
  ex.extra.ask = { tab: 'model', subject: sm
    ? { kind: 'submesh', guid: v.value.guid, submesh: sel.value, look: look.value, label: `submesh ${sel.value} of model ${v.value.guid} (material slot ${sm.material}${v.value.slots[sm.material]?.material ? ' → ' + v.value.slots[sm.material].material : ''})` }
    : { kind: 'model', guid: v.value.guid, look: look.value, label: `model ${v.value.guid}${look.value ? ' with look ' + look.value : ''}` } }
}, { immediate: true })
watch(() => ex.instance, (i) => { if (v.value && i >= 0 && i < v.value.submeshes.length) sel.value = i })

const lodLabel = (l: any, i: number) => (l.mask === 255 ? 'all' : `LOD${i}`)
const maskText = (m: number) => (m === 255 ? 'all' : `LOD${Math.log2(m)}`)
const tris = (sm: any) => Math.floor(sm.indexCount / 3)
const selSm = computed(() => (v.value && sel.value >= 0 ? v.value.submeshes[sel.value] : null))

async function exportAs(fmt: string) {
  try {
    const path = await api.ExportModel(v.value.guid, look.value, v.value.lods[lod.value]?.mask ?? 255, fmt)
    note.value = path ? `Saved ${path}` : ''
  } catch (e: any) { note.value = String(e) }
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <div class="flex min-w-0 flex-1 flex-col">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-1.5 text-[12px]">
        <div v-if="v" class="flex overflow-hidden rounded border border-line">
          <button v-for="(l, i) in v.lods" :key="i" class="px-2 py-0.5" :class="lod === i ? 'bg-raised text-fg' : 'text-dim'" :title="`${num(l.triangles)} triangles, ${l.submeshes} submeshes`" @click="lod = i">{{ lodLabel(l, i) }}</button>
        </div>
        <div class="flex overflow-hidden rounded border border-line">
          <button v-for="m in (['texture', 'material', 'normal', 'plain'] as const)" :key="m" class="px-2 py-0.5" :class="shade === m ? 'bg-raised text-fg' : 'text-dim'" :title="m === 'material' ? 'a colour per material slot' : ''" @click="shade = m">{{ m === 'material' ? 'slots' : m }}</button>
        </div>
        <div v-if="shade === 'texture'" class="flex overflow-hidden rounded border border-line" title="simple: the colour texture, Lambert (T064). material: each slot material's PBR inputs (colour, normal, roughness, metal, specular, AO, emissive, alpha) from the Material tab's mapping, lit by a studio environment + sun (not the game's lighting). Off on every open.">
          <button class="px-2 py-0.5" :class="!pbr ? 'bg-raised text-fg' : 'text-dim'" @click="setPBR(0)">simple</button>
          <button class="px-2 py-0.5" :class="pbr ? 'bg-raised text-fg' : 'text-dim'" @click="setPBR(1)">material (PBR)</button>
        </div>
        <label class="flex items-center gap-1 text-dim"><input v-model="wire" type="checkbox">wire</label>
        <label v-if="v?.skeleton" class="flex items-center gap-1 text-dim" :title="`${v.skeleton.bones.length} bones (bind pose)`"><input v-model="bones" type="checkbox">skeleton</label>
        <label class="flex items-center gap-1 text-dim" title="Mirror X (the game's axes are left-handed)"><input v-model="mirror" type="checkbox">mirror</label>
        <label class="flex items-center gap-1 text-dim" title="Also draw the proxy submeshes (flag 0x10): coarse copies of the model the game doesn't show (AssetFormats.md §2.5)"><input v-model="proxy" type="checkbox">proxy</label>
        <button class="text-dim hover:text-fg" @click="frame">Fit</button>
        <span class="ml-auto flex items-center gap-2">
          <button class="rounded border border-line px-2 py-0.5 text-dim hover:text-fg" title="Binary glTF, textures inside" @click="exportAs('glb')">Export glTF</button>
          <button class="rounded border border-line px-2 py-0.5 text-dim hover:text-fg" title="OBJ + MTL + PNG textures" @click="exportAs('obj')">Export OBJ</button>
        </span>
      </div>
      <div class="relative min-h-0 flex-1">
        <div ref="wrap" class="absolute inset-0" />
        <div class="pointer-events-none absolute bottom-2 left-3 text-[11.5px] text-dim">
          <span v-if="loading">loading…</span>
          <span v-else>{{ num(stats.verts) }} vertices · {{ num(stats.tris) }} triangles · {{ stats.groups }} submeshes · drag: rotate, right-drag: pan, wheel: zoom, click: select a submesh</span>
        </div>
        <p v-if="err" class="absolute top-3 left-3 max-w-[70%] rounded bg-panel/90 p-2 text-[12px] text-v-err">{{ err }}</p>
        <p v-if="note" class="absolute top-3 right-3 max-w-[60%] rounded bg-panel/90 p-2 text-[12px] text-dim">{{ note }}</p>
      </div>
    </div>
    <Splitter id="model.side" :def="360" :min="240" :max="900" side="after" />
    <aside v-if="v" class="selectable min-h-0 shrink-0 overflow-auto border-l border-line p-4 text-[12.5px]" :style="{ width: sideW + 'px' }">
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        <dt class="text-dim">Model</dt><dd><Guid :guid="v.guid" /></dd>
        <dt class="text-dim">Size</dt><dd>{{ bytes(v.size) }} (CPU part {{ bytes(v.split[0]) }})</dd>
        <dt class="text-dim">LODs</dt><dd><span v-for="(l, i) in v.lods" :key="i" class="mr-2">{{ lodLabel(l, i) }}: {{ num(l.triangles) }} △</span></dd>
        <dt class="text-dim">Look</dt>
        <dd>
          <select v-if="v.looks.length" class="sel mono w-full rounded border border-line bg-well px-1.5 py-0.5 text-[11.5px] text-fg outline-none focus:border-focus" :value="look" @change="chooseLook(($event.target as HTMLSelectElement).value)">
            <option v-for="l in v.looks" :key="l.guid" :value="l.guid">{{ l.guid }} · {{ l.source }} · {{ l.resolved }}/{{ v.slots.length }}{{ l.name ? ' · ' + l.name : '' }}</option>
          </select>
          <span v-else class="text-faint">no look found</span>
          <div v-if="look" class="mt-0.5"><Guid :guid="look" /></div>
        </dd>
      </dl>

      <div v-if="selSm" class="mt-3 rounded border border-focus/40 bg-sel/40 p-2">
        <div class="flex items-center"><b>Submesh {{ sel }}</b><button class="ml-auto text-dim hover:text-fg" title="Clear the selection" @click="sel = -1">✕</button></div>
        <div class="mt-1 text-dim">{{ num(tris(selSm)) }} triangles · VB {{ selSm.vb }} · IB {{ selSm.ib }} · base vertex {{ selSm.vertexStart }} · first index {{ selSm.indexStart }} · {{ maskText(selSm.lod) }} · flags 0x{{ selSm.flags.toString(16) }}</div>
        <div class="mt-1">slot {{ selSm.material }} → <Guid v-if="v.slots[selSm.material]?.material" :guid="v.slots[selSm.material].material" /><span v-else class="text-faint">no entry in the look</span></div>
      </div>

      <h4 class="mt-4 mb-1 text-dim">Material slots ({{ v.slots.length }})</h4>
      <div v-for="(sl, i) in v.slots" :key="i" class="flex items-center gap-2 border-t border-line/50 py-1">
        <span class="h-2.5 w-2.5 shrink-0 rounded-full" :style="{ background: '#' + slotColor(i).getHexString() }" />
        <img v-if="sl.color" :src="`/thumb/${sl.color}.png`" class="h-8 w-8 shrink-0 rounded-sm object-cover" loading="lazy">
        <div class="min-w-0">
          <div class="mono text-[11px] text-faint">{{ i }} · key {{ sl.key }} · {{ sl.submeshes }} submesh{{ sl.submeshes === 1 ? '' : 'es' }}<span v-if="sl.helper" class="ml-1 rounded bg-v-err/20 px-1 text-v-err" title="Helper / editor material (+0x68 bit 1): not drawn in game [?]">helper</span><span v-if="sl.decal" class="ml-1 rounded bg-raised px-1" title="An opacity mask but no colour texture (a normal-only decal): not drawn by the preview">decal</span><span v-else-if="sl.opacity" class="ml-1 rounded bg-raised px-1" title="Drawn with its opacity mask as alpha">alpha</span></div>
          <Guid v-if="sl.material" :guid="sl.material" /><span v-else class="text-faint">no entry in the look</span>
          <span v-if="sl.color" class="ml-1 text-faint">colour <Guid :guid="sl.color" /></span>
        </div>
      </div>

      <h4 class="mt-4 mb-1 text-dim">Submeshes ({{ v.submeshes.length }})</h4>
      <table class="w-full text-[11.5px]">
        <thead class="text-faint"><tr><th class="text-left">#</th><th class="text-right">△</th><th>VB</th><th>IB</th><th>slot</th><th>LOD</th></tr></thead>
        <tbody>
          <tr v-for="(sm, i) in v.submeshes" :key="i" class="cursor-pointer hover:bg-raised" :class="i === sel ? 'bg-sel' : ''" @click="selectSubmesh(i)">
            <td>{{ i }}<span v-if="sm.flags & 0x10" class="ml-1 text-faint" title="Proxy submesh (flag 0x10): not drawn unless “proxy” is on">p</span></td><td class="text-right">{{ num(tris(sm)) }}</td><td class="text-center">{{ sm.vb }}</td><td class="text-center">{{ sm.ib }}</td><td class="text-center">{{ sm.material }}</td><td class="text-center">{{ maskText(sm.lod) }}</td>
          </tr>
        </tbody>
      </table>

      <h4 class="mt-4 mb-1 text-dim">Vertex buffers ({{ v.vbs.length }})</h4>
      <div v-for="(vb, i) in v.vbs" :key="i" class="border-t border-line/50 py-1">
        <div>{{ i }}: {{ num(vb.count) }} vertices, strides {{ vb.strides[0] }} / {{ vb.strides[1] }} <span class="mono text-faint">{{ vb.hash }}</span></div>
        <div class="mono text-[11px] text-dim">{{ vb.elements.map((e: any) => `${e.semantic}${e.index ? e.index : ''}:${e.format}@${e.stream}+${e.offset}`).join('  ') }}</div>
      </div>
      <div class="mt-1 text-dim">Index buffers: {{ v.ibs.map((n: number) => num(n)).join(', ') }}</div>

      <template v-if="v.skeleton">
        <h4 class="mt-4 mb-1 text-dim">Skeleton ({{ v.skeleton.bones.length }} bones, hash {{ v.skeleton.hash }})</h4>
        <div class="max-h-48 overflow-auto mono text-[11px]">
          <div v-for="(b, i) in v.skeleton.bones" :key="i">{{ i }} ← {{ b.parent }} · id {{ b.id }} · ({{ b.pos.map((x: number) => x.toFixed(2)).join(', ') }})</div>
        </div>
      </template>
      <template v-if="v.refs?.length">
        <h4 class="mt-4 mb-1 text-dim">Requested by the loader</h4>
        <div v-for="g in v.refs" :key="g"><Guid :guid="g" /></div>
      </template>
    </aside>
  </div>
</template>

<style scoped>
.sel { appearance: none; -webkit-appearance: none; color-scheme: dark; padding-right: 18px;
  background-image: linear-gradient(45deg, transparent 50%, #8a98b0 50%), linear-gradient(135deg, #8a98b0 50%, transparent 50%);
  background-position: calc(100% - 9px) 50%, calc(100% - 5px) 50%; background-size: 4px 4px; background-repeat: no-repeat; }
.sel option { background: #111827; color: #d8e0ec; }
</style>
