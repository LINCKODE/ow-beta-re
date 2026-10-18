<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { mainPane, mcpDrive, mcpSnapshot, useExplorer } from '~/composables/useExplorer'
import { api, onEvent } from '~/composables/api'
import { usePanel } from '~/composables/usePanel'
import { ask, askStart, askToggle } from '~/composables/useAsk'

const ex = useExplorer()
const { s, refresh } = ex
// keys act on the focused pane (split view)
const fx = () => useExplorer(s.focus ?? mainPane)
const top = ref<any>()
let poll: any

onMounted(async () => {
  await refresh()
  let link = await api.StartupLink()
  const follow = () => { if (link && s.status?.open) { ex.openLink(link); link = '' } }
  follow()
  onEvent('opened', async () => { await refresh(); follow() })
  onEvent('index', async () => {
    await refresh()
    s.listVersion++
    // counts and names in the header come from the index
    if (s.current && s.status?.index?.phase === 'done') { const r = await api.Resource(s.current); if (r.guid === s.current) s.resource = r }
  })
  // The status bar shows the background index's progress.
  poll = setInterval(async () => {
    if (!s.status?.open || !['done', 'failed'].includes(s.status.index?.phase)) { await refresh(); follow() }
  }, 700)
  window.addEventListener('keydown', key)
  window.addEventListener('mouseup', mouse)
  // a document tab's context menu closes on any click outside it / Esc
  window.addEventListener('mousedown', (e) => { if (s.tabMenu && !(e.target as HTMLElement)?.closest?.('[data-tab-menu]')) s.tabMenu = null }, true)
  window.addEventListener('keydown', (e) => { if (e.key === 'Escape') s.tabMenu = null })
  // MCP server (docs/Explorer.md "MCP server"): push what the window shows, apply Claude's drive commands
  s.mcp = await api.McpStatus().catch(() => null)
  onEvent('mcp', (info: any) => { s.mcp = info })
  onEvent('mcp:drive', mcpDrive)
  askStart() // replies arriving while the Ask panel is collapsed count on its thumb
  let pending: any = null
  watch(mcpSnapshot, (v) => {
    clearTimeout(pending)
    pending = setTimeout(() => { try { api.SetView(JSON.stringify(v)) } catch {} }, 120)
  }, { deep: true, immediate: true })
})
onBeforeUnmount(() => { clearInterval(poll); window.removeEventListener('keydown', key); window.removeEventListener('mouseup', mouse) })

// Mouse back / forward buttons (DOM buttons 3 and 4).
function mouse(e: MouseEvent) {
  if (e.button === 3) { e.preventDefault(); fx().back() } else if (e.button === 4) { e.preventDefault(); fx().forward() }
}

function key(e: KeyboardEvent) {
  const typing = ['INPUT', 'TEXTAREA'].includes((e.target as HTMLElement)?.tagName)
  const ctrl = e.ctrlKey || e.metaKey
  if ((e.key === '/' && !typing) || (e.key.toLowerCase() === 'k' && ctrl)) {
    e.preventDefault()
    top.value?.focus()
  }
  const f = fx(), p = f.s
  if (e.altKey && e.key === 'ArrowLeft') { e.preventDefault(); f.back() }
  else if (e.altKey && e.key === 'ArrowRight') { e.preventDefault(); f.forward() }
  else if (!typing && e.key === 'Backspace') { e.preventDefault(); f.back() }
  else if (ctrl && e.key.toLowerCase() === 'w') { e.preventDefault(); if (p.docs.length) f.closeDoc(p.active) }
  else if (ctrl && e.key === 'Tab') {
    e.preventDefault()
    if (p.docs.length > 1) f.activate((p.active + (e.shiftKey ? p.docs.length - 1 : 1)) % p.docs.length)
  } else if (ctrl && /^[1-9]$/.test(e.key)) { e.preventDefault(); f.activate(Math.min(Number(e.key), p.docs.length) - 1) }
  else if (!typing && !ctrl && e.key.toLowerCase() === 'p') f.togglePin()
  else if (!typing && !ctrl && e.key === '\\') s.wide = !s.wide
  else if (ctrl && e.key.toLowerCase() === 'j') { e.preventDefault(); askToggle() }
}
const railW = usePanel('rail', 264)
const listW = usePanel('list', 360)
const splitW = usePanel('split', 640)
// Ask Claude panel: docked on the right, collapsible (thumb on the window edge, Ctrl+J); open state and width remembered
const askW = usePanel('ask.width', 420)
const askOpen = usePanel('ask.open', 0) // 1 = expanded (SetPanel keeps positive values only)
let askLoaded = false
watch(() => s.ui.panels?.['ask.open'], () => {
  if (askLoaded || !s.status) return
  askLoaded = true
  if (askOpen.value) { ask.open = true; askStart() }
})
watch(() => ask.open, (o) => { askOpen.value = o ? 1 : 0; askOpen.commit(!o) })
</script>

<template>
  <div class="grid h-full grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
    <TopBar ref="top" />
    <main class="flex min-h-0 min-w-0 overflow-hidden">
      <Rail v-show="!s.wide" class="shrink-0" :style="{ width: railW + 'px' }" />
      <Splitter v-show="!s.wide" id="rail" :def="264" :min="180" :max="600" />
      <ResourceList v-show="!s.wide" class="shrink-0" :style="{ width: listW + 'px' }" />
      <Splitter v-show="!s.wide" id="list" :def="360" :min="240" :max="1000" />
      <PaneHost :pane="mainPane" class="min-w-0 flex-1" />
      <template v-if="s.split">
        <Splitter id="split" :def="640" :min="320" :max="2000" side="after" />
        <PaneHost :key="s.split.id" :pane="s.split" class="shrink-0 border-l border-line-strong" :style="{ width: splitW + 'px' }" />
      </template>
      <template v-if="ask.open">
        <Splitter id="ask.width" :def="420" :min="300" :max="1000" side="after" />
        <AskPanel class="shrink-0" :style="{ width: askW + 'px' }" />
      </template>
    </main>
    <StatusBar />
    <GuidMenu />
    <div v-if="s.docDrag" class="pointer-events-none fixed z-50 rounded border border-focus/60 bg-raised px-2 py-1 text-[12px] shadow-xl" :style="{ left: s.docDrag.x + 12 + 'px', top: s.docDrag.y + 8 + 'px' }">{{ s.docDrag.label }}</div>
    <button
      class="fixed z-30 flex w-[18px] -translate-y-1/2 flex-col items-center gap-1 rounded-l-md border border-r-0 border-line-strong bg-raised py-2 text-[11px] text-focus shadow-lg shadow-black/40 hover:bg-sel"
      :style="{ right: (ask.open ? askW : 0) + 'px', top: '50%' }"
      :title="(ask.open ? 'Collapse' : 'Expand') + ' the Ask Claude panel (Ctrl+J)'" @click="askToggle"
    >
      <span>{{ ask.open ? '›' : '‹' }}</span>
      <span class="[writing-mode:vertical-rl]">✦ Ask</span>
      <span v-if="!ask.open && ask.unread" class="mono rounded-full bg-v-err px-1 text-[10px] leading-4 text-ink" :title="`${ask.unread} new repl${ask.unread > 1 ? 'ies' : 'y'}`">{{ ask.unread }}</span>
    </button>
    <div v-if="s.toast" class="mono pointer-events-none fixed bottom-10 left-1/2 z-50 -translate-x-1/2 rounded-md border border-line-strong bg-raised px-3 py-1.5 text-[12px] shadow-xl">{{ s.toast }}</div>
  </div>
</template>
