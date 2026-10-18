// Map lighting (T071 round 3): a map's own environment (Go: app_env.go, App.MapEnvironment) as three.js objects: the sun
// (direction, HDR colour), the sky cube as an HDR equirectangular background + image-based lighting (PMREM), the
// ambient cube's upper / lower average colours (a hemisphere light for Lambert materials) and the colour-grading LUT
// (1024×32 strip → 32³ Data3DTexture: red across a slice, green down it, blue across slices; [D] T071 report).
// Not the game's lighting pipeline: no baked GI / lightmaps / probes (Rendering.md §8).
import * as THREE from 'three'
import { api } from '~/composables/api'

export interface MapEnv {
  sunDir: THREE.Vector3            // the direction sunlight travels (unit, down)
  sunColor: THREE.Color            // HDR (Anubis 3.29, 2.23, 1.61)
  sky: THREE.DataTexture | null    // equirectangular HDR (background)
  env: THREE.Texture | null        // PMREM of the ambient cube (scene.environment)
  bounce: THREE.Color              // warm light from below (an estimate of the ground bounce; linear)
  lut: THREE.Data3DTexture | null
  hemiSky: THREE.Color             // ambient cube, upper half average (linear)
  hemiGround: THREE.Color          // lower half
  raw: any
}

async function panorama(guid: string, w: number) {
  const r = await fetch(`/envmap/${guid}.bin?w=${w}`)
  if (!r.ok) throw new Error(await r.text())
  const buf = await r.arrayBuffer()
  const dv = new DataView(buf)
  const W = dv.getUint32(4, true), H = dv.getUint32(8, true)
  return { w: W, h: H, px: new Float32Array(buf, 12, W * H * 4) }
}

function halfTexture(p: { w: number; h: number; px: Float32Array }) {
  const d = new Uint16Array(p.px.length)
  for (let i = 0; i < d.length; i++) d[i] = THREE.DataUtils.toHalfFloat(Math.min(p.px[i], 65000))
  const t = new THREE.DataTexture(d, p.w, p.h, THREE.RGBAFormat, THREE.HalfFloatType)
  t.mapping = THREE.EquirectangularReflectionMapping
  t.colorSpace = THREE.LinearSRGBColorSpace
  t.magFilter = t.minFilter = THREE.LinearFilter
  t.needsUpdate = true
  return t
}

async function lut3d(guid: string): Promise<THREE.Data3DTexture> {
  const img = new Image()
  img.src = `/tex/${guid}/0/0.png`
  await img.decode()
  const c = document.createElement('canvas')
  c.width = img.width; c.height = img.height
  const g = c.getContext('2d', { willReadFrequently: true })!
  g.drawImage(img, 0, 0)
  const src = g.getImageData(0, 0, img.width, img.height).data
  const N = img.height // 32
  const data = new Uint8Array(N * N * N * 4)
  for (let b = 0; b < N; b++) for (let y = 0; y < N; y++) for (let x = 0; x < N; x++) {
    const s = (y * img.width + b * N + x) * 4, o = (x + y * N + b * N * N) * 4
    data[o] = src[s]; data[o + 1] = src[s + 1]; data[o + 2] = src[s + 2]; data[o + 3] = 255
  }
  const t = new THREE.Data3DTexture(data, N, N, N)
  t.format = THREE.RGBAFormat
  t.type = THREE.UnsignedByteType
  t.minFilter = t.magFilter = THREE.LinearFilter
  t.wrapS = t.wrapT = t.wrapR = THREE.ClampToEdgeWrapping
  t.unpackAlignment = 1
  t.needsUpdate = true
  return t
}

export async function loadMapEnvironment(renderer: THREE.WebGLRenderer, map: string): Promise<MapEnv> {
  const e = await api.MapEnvironment(map)
  const out: MapEnv = {
    sunDir: new THREE.Vector3(...(e.sunDir as [number, number, number])).normalize(),
    sunColor: new THREE.Color(e.sunColor[0], e.sunColor[1], e.sunColor[2]),
    sky: null, env: null, bounce: new THREE.Color(0, 0, 0), lut: null,
    hemiSky: new THREE.Color(0.35, 0.45, 0.6), hemiGround: new THREE.Color(0.3, 0.25, 0.2), raw: e,
  }
  // ambient: the ambient cube (sky light, its own brightness) as image-based lighting; plus a warm bounce term from below
  // (sun colour × an assumed ground albedo 0.5 × the sun's elevation / π) standing in for the game's baked GI, whose
  // data we can't read yet [? inferred: in game, surfaces in shadow are warm, not sky-blue]. The sky cube (much brighter:
  // its sunlit sand ≈ 1.5) is the background only; as lighting it washed everything out.
  const jobs: Promise<void>[] = []
  if (e.sky) jobs.push(panorama(e.sky, 512).then((p) => { out.sky = halfTexture(p) }).catch(() => {}))
  if (e.ambient) jobs.push(panorama(e.ambient, 64).then((p) => {
    const t = halfTexture(p)
    const pm = new THREE.PMREMGenerator(renderer)
    out.env = pm.fromEquirectangular(t).texture
    pm.dispose()
    t.dispose()
    const up = [0, 0, 0], dn = [0, 0, 0]
    let nu = 0, nd = 0
    for (let j = 0; j < p.h; j++) for (let i = 0; i < p.w; i++) {
      const o = (j * p.w + i) * 4, a = j >= p.h / 2 ? up : dn
      for (let k = 0; k < 3; k++) a[k] += p.px[o + k]
      if (j >= p.h / 2) nu++; else nd++
    }
    out.hemiSky.setRGB(up[0] / nu, up[1] / nu, up[2] / nu)
    out.hemiGround.setRGB(dn[0] / nd, dn[1] / nd, dn[2] / nd)
  }).catch(() => {}))
  const elev = Math.max(0, -out.sunDir.y)
  out.bounce.copy(out.sunColor).multiplyScalar(0.5 * elev / Math.PI)
  if (e.lut) jobs.push(lut3d(e.lut).then((t) => { out.lut = t }).catch(() => {}))
  await Promise.all(jobs)
  return out
}

export function disposeMapEnvironment(e: MapEnv | null) {
  e?.sky?.dispose(); e?.env?.dispose(); e?.lut?.dispose()
}
