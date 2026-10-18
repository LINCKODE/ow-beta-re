// Material builder (T071): a game material's resolved preview (Go: internal/model/preview.go, App.MaterialPreviews /
// App.Material) → a three.js material, shared by the Material tab's preview, the Model tab's and the 3D map's
// "material (PBR)" mode. The textures are the per-input images of /mprev/<material>/<input>.png (colour and emissive
// sRGB, normal encoded, the scalars grey; "orm" packs AO / roughness / metalness in R / G / B for the map).
// docs/Explorer.md "Material view".
import * as THREE from 'three'
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js'

export type Preview = {
  family: { id: number; name: string; mode: string; geometry: string; conf: string }
  inputs: { input: string; texture: string; slot: string; slotName?: string; channel: string; format?: string; uvSet: number; on: boolean; intensity?: number; conf: string; note?: string }[]
  uvScale: [number, number]
  uvOffset: [number, number]
  params: any[]
  notes: string[]
}

export interface BuildOpts {
  size: number                     // texture size cap (largest mip ≤ size)
  context: 'preview' | 'model' | 'map'
  physical?: boolean               // MeshPhysicalMaterial (specular map, transmission); else MeshStandardMaterial
  orm?: boolean                    // one packed AO / roughness / metalness texture (fewer textures: the map)
  on?: Record<string, boolean>     // input toggles (default: each input's own "on": off for inputs on UV1+)
  tiling?: number                  // extra UV repeat (the preview)
  useUV?: boolean                  // the material's UV scale / offset (default true)
  flipGreen?: boolean
  envIntensity?: number
  side?: THREE.Side
  uv1?: boolean                    // the geometry has UV1 (models, the map): inputs the game reads on UV1 are used too
}

const loader = new THREE.TextureLoader()

export class MaterialBuilder {
  textures = new Map<string, THREE.Texture>()
  pending = 0
  constructor(private onLoad: () => void = () => {}) {}

  // one texture per (material, input, colour space, size); D3D coordinates (flipY = false), wrap addressing
  tex(material: string, input: string, srgb: boolean, size: number) {
    const key = `${material}/${input}/${srgb}/${size}`
    let t = this.textures.get(key)
    if (!t) {
      this.pending++
      const done = () => { this.pending--; this.onLoad() }
      t = loader.load(`/mprev/${material}/${input}.png?max=${size}`, done, undefined, done)
      t.colorSpace = srgb ? THREE.SRGBColorSpace : THREE.NoColorSpace
      t.flipY = false
      t.wrapS = t.wrapT = THREE.RepeatWrapping
      t.anisotropy = 4
      this.textures.set(key, t)
    }
    return t
  }

  // build: null = not drawn (a normal-only decal on a model or map: it would cover what is under it with grey)
  build(material: string, p: Preview, o: BuildOpts): THREE.Material | null {
    const has = (i: string) => p.inputs.some((x) => x.input === i)
    const inp = (i: string) => p.inputs.find((x) => x.input === i)
    const onUV1 = (i: string) => !!o.uv1 && inp(i)?.uvSet === 1
    const use = (i: string) => has(i) && (o.on?.[i] ?? (inp(i)!.on || onUV1(i)))
    const rep = o.tiling ?? 1
    const uv = o.useUV === false ? [1, 1] : p.uvScale
    const of = o.useUV === false ? [0, 0] : p.uvOffset
    const T = (i: string, srgb: boolean) => {
      const t = this.tex(material, i, srgb, o.size)
      t.repeat.set(rep * uv[0], rep * uv[1])
      t.offset.set(of[0], of[1])
      if (onUV1(i)) t.channel = 1 // three's second UV set (attribute uv1)
      return t
    }
    const side = o.side ?? THREE.DoubleSide
    const m = p.family.mode
    const map = use('color') ? T('color', true) : null
    const alphaMap = use('opacity') ? T('opacity', false) : null
    if (m === 'decal' && !map && o.context !== 'preview') return null
    if (m === 'effect') {
      return new THREE.MeshBasicMaterial({ map, alphaMap, color: map ? 0xffffff : 0x808080, transparent: true, depthWrite: false,
        blending: THREE.AdditiveBlending, side })
    }
    if (m === 'unlit' || m === 'sky') {
      return new THREE.MeshBasicMaterial({ map, alphaMap, transparent: !!alphaMap, side, color: map ? 0xffffff : 0x808080 })
    }
    const x: any = { color: map ? 0xffffff : 0x8a8a8a, map, roughness: 0.6, metalness: 0, side, envMapIntensity: o.envIntensity ?? 1 }
    if (use('normal')) { x.normalMap = T('normal', false); x.normalScale = new THREE.Vector2(1, o.flipGreen ? -1 : 1) }
    if (o.orm) { // orm packs AO only when it is on UV0 (Go ormImage); AO on UV1 gets its own texture on channel 1
      const r = use('roughness'), mt = use('metalness'), ao = use('ao') && !onUV1('ao')
      if (r || mt || ao) {
        const t = T('orm', false)
        if (r) { x.roughnessMap = t; x.roughness = 1 }
        if (mt) { x.metalnessMap = t; x.metalness = 1 }
        if (ao) { x.aoMap = t; x.aoMapIntensity = 1 }
      }
      if (use('ao') && onUV1('ao')) { x.aoMap = T('ao', false); x.aoMapIntensity = 1 }
    } else {
      if (use('roughness')) { x.roughnessMap = T('roughness', false); x.roughness = 1 }
      if (use('metalness')) { x.metalnessMap = T('metalness', false); x.metalness = 1 }
      if (use('ao')) { x.aoMap = T('ao', false); x.aoMapIntensity = 1 }
      // specular = dielectric F0 / 0.08 → specularColorMap × 2 (three's F0 = 0.04 × colour × map)
      if (o.physical && use('specular')) { x.specularColorMap = T('specular', false); x.specularColor = new THREE.Color(2, 2, 2) }
    }
    if (use('emissive')) { // the image is already mask × albedo (Go); × the material's intensity parameter
      x.emissiveMap = T('emissive', true)
      x.emissive = new THREE.Color(0xffffff)
      x.emissiveIntensity = inp('emissive')?.intensity ?? 1
    }
    if (m === 'decal' || m === 'alphatest') {
      if (alphaMap) x.alphaMap = alphaMap
      if (m === 'alphatest') x.alphaTest = 0.5
      else { x.transparent = true; x.depthWrite = false; x.polygonOffset = true; x.polygonOffsetFactor = -2; x.polygonOffsetUnits = -2 }
    }
    if (m === 'glass') {
      if (o.physical) Object.assign(x, { transmission: 0.9, thickness: 0.5, roughness: 0.05, ior: 1.33 })
      else Object.assign(x, { transparent: true, opacity: 0.35, depthWrite: false, roughness: 0.05 })
    }
    const mat = o.physical ? new THREE.MeshPhysicalMaterial(x) : new THREE.MeshStandardMaterial(x)
    patchLayers(mat, material, p, this, o.size)
    return mat
  }

  dispose() {
    this.textures.forEach((t) => t.dispose())
    this.textures.clear()
  }
}

// ---- layered materials (T071 round 4, bytecode [D]):
//   0x25: albedo = lerp(basecolor, colourB × D44A3918, t), t = sat((height − (1 − vertex.a)) / clamp(vertex.g, 0.01, 1)
//         + vertex.a); colourB and the height map on uv0 × C251F501 + 811D50FA. (Its spec/gloss B and secnormal are not
//         blended here.)
//   0x24: albedo = basecolor × overlay × 2, the overlay on (859FBB9D: 1 uv0, 2 uv1, else 0) × 656F1D9A + 9432648A.
// The mesh brings the vertex colours (owColor) and UV1 (owUv1) (OWMD v2, utils/owmd.ts). The patch runs after three's
// map_fragment, so it works on MeshLambertMaterial (simple) and MeshStandard / Physical (material) alike.
// addShaderPatch chains shader patches on one material (onBeforeCompile + the program cache key): layers, probes.
type Patch = (sh: THREE.WebGLProgramParametersWithUniforms) => void
export function addShaderPatch(mat: THREE.Material, key: string, fn: Patch) {
  const d = mat.userData as { owPatches?: { key: string; fn: Patch }[] }
  d.owPatches = (d.owPatches ?? []).filter((x) => x.key !== key).concat({ key, fn })
  mat.onBeforeCompile = (sh) => { for (const x of d.owPatches!) x.fn(sh) }
  mat.customProgramCacheKey = () => d.owPatches!.map((x) => x.key).join('+')
  mat.needsUpdate = true
}

const param = (p: Preview, h: string) => p.params.find((x: any) => x.hash === h)?.values as number[] | undefined
export function layerKind(p: Preview | undefined): '' | 'blend' | 'overlay' {
  if (!p) return ''
  const has = (i: string) => p.inputs.some((x) => x.input === i)
  if (p.family.id === 0x25 && has('colorB') && has('height')) return 'blend'
  if (p.family.id === 0x24 && has('overlay')) return 'overlay'
  return ''
}
export function patchLayers(mat: THREE.Material, guid: string, p: Preview | undefined, b: MaterialBuilder, size: number) {
  const kind = layerKind(p)
  if (!kind || !p) return false
  const u: Record<string, THREE.IUniform> = {}
  let frag = ''
  if (kind === 'blend') {
    const sc = param(p, 'c251f501') ?? [1, 1]
    u.owColorB = { value: b.tex(guid, 'colorB', true, size) }
    u.owHeight = { value: b.tex(guid, 'height', false, size) }
    u.owUvB = { value: new THREE.Vector4(sc[0], sc[1], p.uvOffset[0], p.uvOffset[1]) }
    u.owMulB = { value: param(p, 'd44a3918')?.[0] ?? 1 }
    frag = `
      vec2 owUvBv = vOwUv0 * owUvB.xy + owUvB.zw;
      float owH = texture2D(owHeight, owUvBv).r;
      float owT = clamp((owH - (1.0 - vOwColor.a)) / clamp(vOwColor.g, 0.01, 1.0) + vOwColor.a, 0.0, 1.0);
      diffuseColor.rgb = mix(diffuseColor.rgb, texture2D(owColorB, owUvBv).rgb * owMulB, owT);`
  } else {
    const sel = param(p, '859fbb9d')?.[0] ?? 0, sc = param(p, '656f1d9a') ?? [1, 1], of = param(p, '9432648a') ?? [0, 0]
    u.owOverlay = { value: b.tex(guid, 'overlay', true, size) }
    u.owUvO = { value: new THREE.Vector4(sc[0], sc[1], of[0], of[1]) }
    u.owSel = { value: sel }
    frag = `
      vec2 owUvOv = (owSel == 1.0 ? vOwUv0 : owSel == 2.0 ? vOwUv1 : vec2(0.0)) * owUvO.xy + owUvO.zw;
      diffuseColor.rgb *= texture2D(owOverlay, owUvOv).rgb * 2.0;`
  }
  addShaderPatch(mat, 'ow-' + kind, (sh) => {
    Object.assign(sh.uniforms, u)
    sh.vertexShader = sh.vertexShader
      .replace('#include <uv_pars_vertex>', '#include <uv_pars_vertex>\nattribute vec4 owColor;\nattribute vec2 owUv1;\nvarying vec4 vOwColor;\nvarying vec2 vOwUv0;\nvarying vec2 vOwUv1;')
      .replace('#include <uv_vertex>', '#include <uv_vertex>\nvOwColor = owColor;\nvOwUv0 = uv;\nvOwUv1 = owUv1;')
    const decl = Object.keys(u).map((k) => `uniform ${k === 'owMulB' || k === 'owSel' ? 'float' : k.startsWith('owUv') ? 'vec4' : 'sampler2D'} ${k};`).join('\n')
    sh.fragmentShader = sh.fragmentShader
      .replace('#include <uv_pars_fragment>', `#include <uv_pars_fragment>\nvarying vec4 vOwColor;\nvarying vec2 vOwUv0;\nvarying vec2 vOwUv1;\n${decl}`)
      .replace('#include <map_fragment>', `#include <map_fragment>\n${frag}`)
  })
  return true
}

// studio lighting: RoomEnvironment through PMREM (generated in code, no downloads). Not the game's probes / lightmaps.
export function studioEnvironment(renderer: THREE.WebGLRenderer) {
  const pmrem = new THREE.PMREMGenerator(renderer)
  const t = pmrem.fromScene(new RoomEnvironment(), 0.04).texture
  pmrem.dispose()
  return t
}
