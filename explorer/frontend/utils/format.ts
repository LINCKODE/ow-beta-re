// Resource type of a GUID: bitreverse12((guid >> 48) & 0xFFF) + 1 (docs/ResourceLoading.md §1): hex digits 1–3.
export function guidType(guid: string): number {
  const top = parseInt(guid.padStart(16, '0').slice(1, 4), 16)
  let r = 0
  for (let i = 0; i < 12; i++) if (top & (1 << i)) r |= 1 << (11 - i)
  return r + 1
}

// One hue per resource type (golden angle), used by every GUID and type swatch.
export function typeHue(t: number): number {
  return Math.round((t * 137.508 + 200) % 360)
}
export function typeColor(t: number, l = 0.8, c = 0.12): string {
  return `oklch(${l} ${c} ${typeHue(t)})`
}
export const hex3 = (t: number) => t.toString(16).toUpperCase().padStart(3, '0')
export const hex8 = (h: number) => (h >>> 0).toString(16).padStart(8, '0')

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB']
  let v = n / 1024, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${u[i]}`
}
export const num = (n: number) => n.toLocaleString('en-US')

// Accepts 0x…, ow://… and bare hex; returns 16 hex digits or null.
export function parseGuid(s: string): string | null {
  const m = s.trim().toLowerCase().replace(/^ow:\/\//, '').replace(/^0x/, '')
  return /^[0-9a-f]{1,16}$/.test(m) ? m.padStart(16, '0') : null
}

// Colours of reference-index edge kinds (by source), used by the References tab and the dependency graph.
const KIND_COLORS: Record<string, string> = {
  'stu.field': '#8fb4ff',
  'map.placement': '#ff8a78', 'map.asset': '#e8a87c', 'map.env': '#e8c46b', 'map.instance': '#f0a0c0',
  'package.member': '#5b6780', 'package.dep': '#7f8ba3', 'package.bundle': '#7f8ba3', 'package.owner': '#7fd6c2',
  'texture.payload': '#b99cff', 'texture.base': '#d98cf0',
  'model.look': '#6fd3e8', 'map.look': '#5fb8d8', 'model.effect': '#9fd0a0', 'material.texture': '#c6a0ff',
  'material.shader': '#a8b4c8', 'material.ref': '#8a98b0',
}
export function kindColor(k: string): string {
  return KIND_COLORS[k] ?? `oklch(0.78 0.1 ${[...k].reduce((a, c) => (a * 31 + c.charCodeAt(0)) % 360, 7)})`
}

// Statescript node kinds and network sides (graph view, Ask badges)
export const NODE_KIND_COLORS: Record<string, string> = {
  Entry: '#8fd694', Condition: '#e8c46b', Action: '#8fb4ff', Terminator: '#ff8a78', State: '#c9b6ff', Other: '#8a98b0',
}
export const NET_COLORS: Record<string, string> = { client: '#eaa9c6', both: '#7fd6c2', server: '#e8a25c' }
export const NET_LABELS: Record<string, string> = { client: 'client only', both: 'client + server', server: 'server only' }
