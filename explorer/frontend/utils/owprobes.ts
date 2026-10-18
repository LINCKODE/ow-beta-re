// Reflection probes (T071 round 5): a map's box-projected reflection probes (map record type 4; Go: app_env.go
// MapProbes, /probes/<map>.bin) replace three's image-based specular (`radiance`) in the 3D map's material mode.
// Per pixel: the smallest probe box containing the point (in game coordinates), the reflection ray intersected with
// that box, and the probe's HDR capture sampled towards the hit point; outside every box the map-wide capture
// (environment +0x130). Roughness picks between three blur levels (panoramas from cube mips 2 / 4 / 6).
// This is the idea of the game's local reflections [? the game's exact blend / fade between probes is not traced].
import * as THREE from 'three'
import { api } from '~/composables/api'
import { addShaderPatch } from '~/utils/owmat'

export interface Probes {
  count: number
  uniforms: Record<string, THREE.IUniform>
  dispose: () => void
}

// probe data texture: 9 RGBA float texels per probe (row): 0–3 the inverse of (translate · rotate · scale) as columns
// (game → probe space), 4–6 rotate · scale as columns (probe → game directions), 7 extents + volume (w = −1: map-wide),
// 8 the bounding sphere (centre, radius²) for a cheap reject before the box test
export async function loadProbes(map: string): Promise<Probes | null> {
  const v = await api.MapProbes(map)
  if (!v?.probes?.length) return null
  const n = v.probes.length
  const data = new Float32Array(9 * 4 * n)
  const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), t = new THREE.Vector3()
  v.probes.forEach((p: any, i: number) => {
    t.set(p.pos[0], p.pos[1], p.pos[2]); q.set(p.rot[0], p.rot[1], p.rot[2], p.rot[3]).normalize(); s.set(p.scale[0] || 1, p.scale[1] || 1, p.scale[2] || 1)
    m.compose(t, q, s)
    const inv = m.clone().invert().elements, rs = m.elements
    const o = i * 36
    for (let c = 0; c < 4; c++) for (let r = 0; r < 4; r++) data[o + c * 4 + r] = inv[c * 4 + r]
    for (let c = 0; c < 3; c++) for (let r = 0; r < 3; r++) data[o + 16 + c * 4 + r] = rs[c * 4 + r]
    const e = p.extents
    data[o + 28] = e[0]; data[o + 29] = e[1]; data[o + 30] = e[2]
    data[o + 31] = p.record < 0 ? -1 : 8 * e[0] * e[1] * e[2] * s.x * s.y * s.z
    const r = Math.hypot(e[0] * s.x, e[1] * s.y, e[2] * s.z)
    data[o + 32] = t.x; data[o + 33] = t.y; data[o + 34] = t.z; data[o + 35] = p.record < 0 ? -1 : r * r
  })
  const dt = new THREE.DataTexture(data, 9, n, THREE.RGBAFormat, THREE.FloatType)
  dt.minFilter = dt.magFilter = THREE.NearestFilter
  dt.needsUpdate = true

  const r = await fetch(`/probes/${map}.bin`)
  if (!r.ok) throw new Error(await r.text())
  const buf = await r.arrayBuffer()
  const dv = new DataView(buf)
  if (dv.getUint32(0, true) !== 0x5250574f) throw new Error('not an OWPR atlas')
  const count = dv.getUint32(4, true), levels = dv.getUint32(8, true)
  let o = 12
  const atlases: THREE.DataTexture[] = []
  for (let l = 0; l < levels; l++) {
    const w = dv.getUint32(o, true); o += 4
    const h = (w / 2) * count
    const px = new Uint16Array(buf.slice(o, o + w * h * 8)); o += w * h * 8
    const a = new THREE.DataTexture(px, w, h, THREE.RGBAFormat, THREE.HalfFloatType)
    a.minFilter = a.magFilter = THREE.LinearFilter
    a.wrapS = THREE.RepeatWrapping
    a.wrapT = THREE.ClampToEdgeWrapping
    a.colorSpace = THREE.LinearSRGBColorSpace
    a.needsUpdate = true
    atlases.push(a)
  }
  const uniforms: Record<string, THREE.IUniform> = {
    owProbeData: { value: dt }, owProbeN: { value: count },
    owProbeAtlas0: { value: atlases[0] }, owProbeAtlas1: { value: atlases[1] }, owProbeAtlas2: { value: atlases[2] },
    owGameFromWorld: { value: new THREE.Matrix4() }, owProbeOn: { value: 1 }, owProbeIntensity: { value: 1 },
  }
  return { count, uniforms, dispose: () => { dt.dispose(); atlases.forEach((a) => a.dispose()) } }
}

const PARS = /* glsl */ `
uniform sampler2D owProbeData;
uniform int owProbeN;
uniform sampler2D owProbeAtlas0;
uniform sampler2D owProbeAtlas1;
uniform sampler2D owProbeAtlas2;
uniform mat4 owGameFromWorld;
uniform float owProbeOn;
uniform float owProbeIntensity;
varying vec3 vOwWorld;
vec3 owTile(sampler2D atl, int tile, vec3 d) {
  vec2 sz = vec2(textureSize(atl, 0));
  float th = sz.y / float(owProbeN);
  d = normalize(d);
  float u = atan(d.z, d.x) * 0.15915494 + 0.5;
  float v = asin(clamp(d.y, -1.0, 1.0)) * 0.31830989 + 0.5;
  float y = (float(tile) * th + clamp(v * th, 0.5, th - 0.5)) / sz.y;
  return texture2D(atl, vec2(u, y)).rgb;
}
vec3 owSampleProbe(int i, vec3 gp, vec3 gr, float rough) {
  vec3 dir = gr;
  vec4 e = texelFetch(owProbeData, ivec2(7, i), 0);
  if (e.w >= 0.0) { // box projection: the reflection ray from the point to the probe box, sampled from the capture point
    mat4 inv = mat4(texelFetch(owProbeData, ivec2(0, i), 0), texelFetch(owProbeData, ivec2(1, i), 0),
                    texelFetch(owProbeData, ivec2(2, i), 0), texelFetch(owProbeData, ivec2(3, i), 0));
    vec3 lp = (inv * vec4(gp, 1.0)).xyz;
    vec3 lr = (inv * vec4(gr, 0.0)).xyz;
    vec3 t1 = (e.xyz - lp) / lr, t2 = (-e.xyz - lp) / lr;
    vec3 tm = max(t1, t2);
    float t = min(min(tm.x, tm.y), tm.z);
    mat3 rs = mat3(texelFetch(owProbeData, ivec2(4, i), 0).xyz, texelFetch(owProbeData, ivec2(5, i), 0).xyz,
                   texelFetch(owProbeData, ivec2(6, i), 0).xyz);
    dir = rs * (lp + t * lr);
  }
  float lv = clamp(rough, 0.0, 1.0) * 2.0;
  vec3 c0 = owTile(owProbeAtlas0, i, dir), c1 = owTile(owProbeAtlas1, i, dir), c2 = owTile(owProbeAtlas2, i, dir);
  return lv < 1.0 ? mix(c0, c1, lv) : mix(c1, c2, lv - 1.0);
}
// the two smallest boxes containing the point, the inner one faded out over the last 1.5 m to its edge (the game's
// probe blend is not traced [?]; this hides the seams at box edges), the map-wide capture outside every box
vec4 owProbe(vec3 gp, vec3 gr, float rough) {
  int b1 = -1, b2 = -1, glob = -1; float v1 = 1e30, v2 = 1e30, f1 = 0.0, f2 = 0.0;
  for (int i = 0; i < 512; i++) {
    if (i >= owProbeN) break;
    vec4 sph = texelFetch(owProbeData, ivec2(8, i), 0);
    if (sph.w < 0.0) { glob = i; continue; }
    vec3 dc = gp - sph.xyz;
    if (dot(dc, dc) > sph.w) continue;
    vec4 e = texelFetch(owProbeData, ivec2(7, i), 0);
    mat4 inv = mat4(texelFetch(owProbeData, ivec2(0, i), 0), texelFetch(owProbeData, ivec2(1, i), 0),
                    texelFetch(owProbeData, ivec2(2, i), 0), texelFetch(owProbeData, ivec2(3, i), 0));
    vec3 d = e.xyz - abs((inv * vec4(gp, 1.0)).xyz);
    if (any(lessThan(d, vec3(0.0)))) continue;
    float fade = clamp(min(min(d.x, d.y), d.z) / 1.5, 0.0, 1.0);
    if (e.w < v1) { b2 = b1; v2 = v1; f2 = f1; b1 = i; v1 = e.w; f1 = fade; }
    else if (e.w < v2) { b2 = i; v2 = e.w; f2 = fade; }
  }
  if (b1 < 0 && glob < 0) return vec4(0.0);
  vec3 outer = glob >= 0 ? owSampleProbe(glob, gp, gr, rough) : vec3(0.0);
  if (b2 >= 0) outer = mix(outer, owSampleProbe(b2, gp, gr, rough), f2);
  if (b1 < 0) return vec4(outer, 1.0);
  return vec4(mix(outer, owSampleProbe(b1, gp, gr, rough), f1), 1.0);
}
`

// patchProbes: the probe radiance replaces three's IBL specular (the ambient diffuse stays the environment's)
export function patchProbes(mat: THREE.Material, pr: Probes) {
  if (!(mat instanceof THREE.MeshStandardMaterial)) return
  addShaderPatch(mat, 'ow-probes', (sh) => {
    Object.assign(sh.uniforms, pr.uniforms)
    sh.vertexShader = sh.vertexShader
      .replace('#include <common>', '#include <common>\nvarying vec3 vOwWorld;')
      .replace('#include <worldpos_vertex>', `#include <worldpos_vertex>
        {
          vec4 owWP = vec4( transformed, 1.0 );
          #ifdef USE_INSTANCING
            owWP = instanceMatrix * owWP;
          #endif
          vOwWorld = ( modelMatrix * owWP ).xyz;
        }`)
    sh.fragmentShader = sh.fragmentShader
      .replace('#include <common>', '#include <common>\n' + PARS)
      .replace('#include <lights_fragment_begin>', `
        if ( owProbeOn > 1.5 ) { // mirror test: every surface a perfect mirror (shows what the probes reflect)
          material.diffuseColor = vec3( 0.0 ); material.roughness = 0.03; material.specularColor = vec3( 1.0 ); material.specularF90 = 1.0;
        }
        #include <lights_fragment_begin>`)
      .replace('#include <lights_fragment_maps>', `#include <lights_fragment_maps>
        #if defined( RE_IndirectSpecular )
          if ( owProbeOn > 0.5 ) {
            vec3 owN = inverseTransformDirection( geometryNormal, viewMatrix );
            vec3 owV = inverseTransformDirection( geometryViewDir, viewMatrix );
            vec3 owR = reflect( -owV, owN );
            vec3 gp = ( owGameFromWorld * vec4( vOwWorld, 1.0 ) ).xyz;
            vec3 gr = normalize( mat3( owGameFromWorld ) * owR );
            vec4 owPr = owProbe( gp, gr, material.roughness );
            if ( owPr.a > 0.0 ) radiance = owPr.rgb * owProbeIntensity;
            // ambient: the local probe's blurriest level towards the normal (≈ irradiance / π) instead of the map-wide
            // ambient cube, so interiors get their own (dark, warm) light, not the sky's
            vec4 owAmb = owProbe( gp, normalize( mat3( owGameFromWorld ) * owN ), 1.0 );
            if ( owAmb.a > 0.0 ) iblIrradiance = PI * owAmb.rgb * owProbeIntensity;
          }
        #endif`)
  })
}
