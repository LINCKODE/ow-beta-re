// Model meshes from the asset server (/model/<guid>.bin, app_model.go meshBinary) and material textures
// (/mtex/<guid>.png) as three.js objects: shared by the Model tab and the 3D map mode's visual geometry (T064).
import * as THREE from 'three'

export interface MeshGroup { index: number; count: number; material: number; submesh: number }

// parseOWMD: "OWMD", version, V, I, G, G × {first index, count, material slot, submesh}, positions, normals, uvs, indices;
// version 2 (T071) then RGBA8 vertex colours (attribute owColor) and UV set 1 (attribute uv1: three's second UV channel).
export function parseOWMD(buf: ArrayBuffer): { geo: THREE.BufferGeometry; groups: MeshGroup[]; verts: number; tris: number } {
  const dv = new DataView(buf)
  if (dv.getUint32(0, true) !== 0x444d574f) throw new Error('not an OWMD mesh')
  const V = dv.getUint32(8, true), I = dv.getUint32(12, true), G = dv.getUint32(16, true)
  let o = 20
  const groups: MeshGroup[] = []
  for (let k = 0; k < G; k++, o += 16) groups.push({ index: dv.getUint32(o, true), count: dv.getUint32(o + 4, true), material: dv.getUint32(o + 8, true), submesh: dv.getUint32(o + 12, true) })
  const pos = new Float32Array(buf, o, 3 * V); o += 12 * V
  const nrm = new Float32Array(buf, o, 3 * V); o += 12 * V
  const uv = new Float32Array(buf, o, 2 * V); o += 8 * V
  const idx = new Uint32Array(buf, o, I); o += 4 * I
  const geo = new THREE.BufferGeometry()
  geo.setAttribute('position', new THREE.BufferAttribute(pos, 3))
  geo.setAttribute('normal', new THREE.BufferAttribute(nrm, 3))
  geo.setAttribute('uv', new THREE.BufferAttribute(uv, 2))
  geo.setIndex(new THREE.BufferAttribute(idx, 1))
  if (dv.getUint32(4, true) >= 2) {
    geo.setAttribute('owColor', new THREE.BufferAttribute(new Uint8Array(buf, o, 4 * V), 4, true)); o += 4 * V
    const uv1 = new THREE.BufferAttribute(new Float32Array(buf, o, 2 * V), 2)
    geo.setAttribute('uv1', uv1)   // three's second UV channel (aoMap.channel = 1)
    geo.setAttribute('owUv1', uv1) // the same data for the layer shader patch (utils/owmat.ts), declared unconditionally
  }
  groups.forEach((g, k) => geo.addGroup(g.index, g.count, k))
  geo.computeBoundingBox()
  geo.computeBoundingSphere()
  return { geo, groups, verts: V, tris: I / 3 }
}

// proxy: also the flag-0x10 proxy submeshes (coarse copies, not drawn in game; AssetFormats.md §2.5)
export async function fetchMesh(guid: string, lod: number, proxy = false) {
  const r = await fetch(`/model/${guid}.bin?lod=${lod}${proxy ? '&proxy=1' : ''}`)
  if (!r.ok) throw new Error(await r.text())
  return parseOWMD(await r.arrayBuffer())
}

// material textures: D3D texture coordinates (origin top-left), so no flip; sRGB colour; wrap addressing
const loader = new THREE.TextureLoader()
export function materialTexture(cache: Map<string, THREE.Texture>, guid: string, max: number, onLoad: () => void) {
  const key = guid + '/' + max
  let t = cache.get(key)
  if (!t) {
    t = loader.load(`/mtex/${guid}.png?max=${max}`, onLoad)
    t.colorSpace = THREE.SRGBColorSpace
    t.flipY = false
    t.wrapS = t.wrapT = THREE.RepeatWrapping
    t.anisotropy = 4
    cache.set(key, t)
  }
  return t
}
