// A small markdown subset for Claude's replies in the Ask panel: paragraphs, headings, bullet / numbered lists, fenced
// code, `code`, **bold**, *italic*. References become links (docs/Explorer.md "Ask Claude"):
// - 16-hex-digit GUIDs (and 0x… forms) → <Guid>;
// - ow://<guid>[/<tab>[/<n>]] (bare or as [text](ow://…)) → opens it: /graph/<node index> centres a Statescript node,
//   /map/<record> a map placement, /tree/<instance> an STU instance, /texture/<mip> a mip;
// - #N → node N of the last Statescript graph (type 0x01B) named before it in the reply, else of the graph the
//   question was about.
import { guidType } from './format'

export type Span =
  | { t: 'text' | 'code' | 'bold' | 'em' | 'guid'; v: string }
  | { t: 'link'; v: string; label: string }
  | { t: 'node'; v: string; n: number; label?: string } // a Statescript node badge: graph GUID, m_nodes index
  | { t: 'ref'; kind: 'map' | 'tree' | 'texture'; v: string; n: number; label?: string } // map record / instance / mip

// refKey identifies what a badge points at (the Ask panel highlights the badges of the current selection)
export function refKey(sp: Span): string {
  if (sp.t === 'node') return `g:${sp.v}:${sp.n}`
  if (sp.t === 'ref') return `${sp.kind[0]}:${sp.v}:${sp.n}`
  return ''
}

// an ow:// link as a span: graph node / map record / tree instance / texture mip badges, a bare resource as a GUID
function linkSpan(url: string, label?: string): Span {
  const m = url.toLowerCase().match(/^ow:\/\/([0-9a-f]{16})(?:\/([a-z]+)(?:\/(\d+))?)?$/)
  if (!m) return { t: 'link', v: url, label: label ?? url }
  const [, g, tab, n] = m
  if (tab === 'graph' && n) return { t: 'node', v: g!, n: Number(n), label }
  if ((tab === 'map' || tab === 'tree' || tab === 'texture') && n) return { t: 'ref', kind: tab, v: g!, n: Number(n), label }
  if (!tab && !label) return { t: 'guid', v: g! }
  return { t: 'link', v: url.toLowerCase(), label: label ?? url.replace(/^ow:\/\//, '') }
}
export type Block = { k: 'p' | 'h' | 'li' | 'pre'; spans: Span[]; text?: string; n?: string }

const REF = /\[([^\]]+)\]\((ow:\/\/[0-9a-fA-F]{16}(?:\/[a-z]+(?:\/\d+)?)?)\)|(ow:\/\/[0-9a-fA-F]{16}(?:\/[a-z]+(?:\/\d+)?)?)|\b(?:0x)?([0-9a-fA-F]{16})\b/g

function refs(s: string, t: 'text' | 'bold' | 'em'): Span[] {
  const out: Span[] = []
  let last = 0
  for (const m of s.matchAll(REF)) {
    if (m.index! > last) out.push({ t, v: s.slice(last, m.index) })
    if (m[2]) out.push(linkSpan(m[2], m[1]!))
    else if (m[3]) out.push(linkSpan(m[3]))
    else out.push({ t: 'guid', v: m[4]!.toLowerCase() })
    last = m.index! + m[0].length
  }
  if (last < s.length) out.push({ t, v: s.slice(last) })
  return out
}

export function inline(s: string): Span[] {
  const out: Span[] = []
  const re = /`([^`]+)`|\*\*([^*]+)\*\*|(?<![\w*])\*([^*\s][^*]*?)\*(?![\w*])/g
  let last = 0
  for (const m of s.matchAll(re)) {
    if (m.index! > last) out.push(...refs(s.slice(last, m.index), 'text'))
    if (m[1] !== undefined) {
      const g = m[1].match(/^(?:0x)?([0-9a-fA-F]{16})$/)
      if (g) out.push({ t: 'guid', v: g[1]!.toLowerCase() })
      else if (/^ow:\/\/[0-9a-fA-F]{16}(\/[a-z]+(\/\d+)?)?$/.test(m[1])) out.push(linkSpan(m[1]))
      else out.push({ t: 'code', v: m[1] })
    } else if (m[2] !== undefined) out.push(...refs(m[2], 'bold'))
    else out.push(...refs(m[3]!, 'em'))
    last = m.index! + m[0].length
  }
  if (last < s.length) out.push(...refs(s.slice(last), 'text'))
  return out
}

const isGraph = (g: string) => guidType(g.padStart(16, '0')) === 0x1b

// nodes turns #N in text into node links, against the last graph named so far (or the default graph).
function nodes(bs: Block[], graph: string) {
  let cur = graph
  for (const b of bs) {
    const out: Span[] = []
    for (const sp of b.spans) {
      if (sp.t === 'guid' && isGraph(sp.v)) cur = sp.v
      if (sp.t === 'link' || sp.t === 'node') { const g = sp.t === 'node' ? sp.v : sp.v.slice(5, 21); if (isGraph(g)) cur = g }
      if ((sp.t === 'text' || sp.t === 'bold' || sp.t === 'em') && cur && /#\d/.test(sp.v)) {
        let last = 0
        // "#7 SetVar": a CamelCase type name right after the number becomes the badge's label (not repeated after it)
        for (const m of sp.v.matchAll(/(?<![\w#])#(\d+)\b(?:\s+([A-Z][A-Za-z0-9]+)\b)?/g)) {
          if (m.index! > last) out.push({ t: sp.t, v: sp.v.slice(last, m.index) })
          out.push({ t: 'node', v: cur, n: Number(m[1]), label: m[2] ? `#${m[1]} ${m[2]}` : undefined })
          last = m.index! + m[0].length
        }
        if (last < sp.v.length) out.push({ t: sp.t, v: sp.v.slice(last) })
      } else out.push(sp)
    }
    b.spans = out
  }
}

// blocks parses a reply; graph = the Statescript graph the question was about ("" = none).
export function blocks(md: string, graph = ''): Block[] {
  const out: Block[] = []
  const lines = md.replace(/\r/g, '').split('\n')
  let para: string[] = []
  const flush = () => { if (para.length) { out.push({ k: 'p', spans: inline(para.join(' ')) }); para = [] } }
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i]!
    if (l.startsWith('```')) {
      flush()
      const code: string[] = []
      for (i++; i < lines.length && !lines[i]!.startsWith('```'); i++) code.push(lines[i]!)
      out.push({ k: 'pre', spans: [], text: code.join('\n') })
      continue
    }
    const h = l.match(/^#{1,6}\s+(.*)/)
    const li = l.match(/^\s*(?:[-*]|(\d+)\.)\s+(.*)/)
    if (h) { flush(); out.push({ k: 'h', spans: inline(h[1]!) }) }
    else if (li) { flush(); out.push({ k: 'li', spans: inline(li[2]!), n: li[1] }) }
    else if (!l.trim()) flush()
    else para.push(l.trim())
  }
  flush()
  nodes(out, graph)
  return out
}
