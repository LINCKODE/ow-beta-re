<script setup lang="ts">
// Open resources as tabs (each with its own history). Middle-click or × closes; Ctrl+W / Ctrl+Tab in app.vue.
// Tabs can be dragged: within the strip to reorder, onto the other pane's tabs or body to move them there (history
// and view state go with them); right-click has the same moves (docs/Explorer.md "Navigation").
import { computed, onBeforeUnmount } from 'vue'
import { moveDoc, moveDocToWindow, paneById, useExplorer } from '~/composables/useExplorer'
import { guidType, typeColor } from '~/utils/format'

const { s, pane, activate, closeDoc, label } = useExplorer()
const guidOf = (d: any) => d.history[d.cursor] ?? ''

// ---- dragging (pointer events: HTML5 drag and drop is unreliable in WebKitGTK) ----
let start: null | { x: number; y: number; i: number } = null
function down(e: MouseEvent, i: number) {
  e.preventDefault() // no text selection: WebKit would start a native drag of it and swallow the mouseup
  activate(i)
  start = { x: e.clientX, y: e.clientY, i }
  window.addEventListener('mousemove', move, true) // capture: views under the pointer stop their own events
  window.addEventListener('mouseup', up, true)
}
// target: a tab (insert before / after it by the pointer's side), else a pane's strip or body (append)
function target(x: number, y: number) {
  const el = document.elementFromPoint(x, y) as HTMLElement | null
  const paneEl = el?.closest('[data-pane-id]') as HTMLElement | null
  if (!paneEl) return null
  const id = Number(paneEl.dataset.paneId)
  const tab = el?.closest('[data-doc-index]') as HTMLElement | null
  if (tab) {
    const r = tab.getBoundingClientRect()
    return { pane: id, index: Number(tab.dataset.docIndex) + (x > r.left + r.width / 2 ? 1 : 0) }
  }
  return { pane: id, index: paneById(id)?.docs.length ?? 0 }
}
function move(e: MouseEvent) {
  if (!start) return
  if (!s.docDrag) {
    if (Math.abs(e.clientX - start.x) + Math.abs(e.clientY - start.y) < 6) return
    const d = s.docs[start.i]
    s.docDrag = { from: pane, index: start.i, label: d && guidOf(d) ? label(guidOf(d)) : 'empty', x: 0, y: 0, over: null }
    document.body.style.cursor = 'grabbing'
    document.body.style.userSelect = 'none'
    window.getSelection()?.removeAllRanges()
  }
  s.docDrag.x = e.clientX
  s.docDrag.y = e.clientY
  s.docDrag.over = target(e.clientX, e.clientY)
}
function up(e: MouseEvent) {
  window.removeEventListener('mousemove', move, true)
  window.removeEventListener('mouseup', up, true)
  const dd = s.docDrag
  start = null
  s.docDrag = null
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  if (!dd) return
  const t = target(e.clientX, e.clientY)
  const to = t ? paneById(t.pane) : null
  if (!to || (to === dd.from && (t!.index === dd.index || t!.index === dd.index + 1))) return
  moveDoc(dd.from, dd.index, to, t!.index)
}
onBeforeUnmount(() => { window.removeEventListener('mousemove', move, true); window.removeEventListener('mouseup', up, true) })
// drop marker: before tab k of this pane (k = docs.length: after the last)
const marker = computed(() => (s.docDrag?.over?.pane === pane.id ? s.docDrag.over.index : -1))

// ---- context menu ----
function menu(e: MouseEvent, i: number) { e.preventDefault(); s.tabMenu = { x: e.clientX, y: e.clientY, pane, index: i } }
const other = computed(() => (pane.id === 1 ? (s.split ? 'the split pane' : 'a new split pane') : 'the main pane'))
</script>

<template>
  <div class="flex h-8 shrink-0 items-end gap-px overflow-x-auto border-b border-line bg-well px-1" role="tablist">
    <button class="mb-0.5 mr-1 h-6 shrink-0 rounded px-1.5 text-[12px] text-dim hover:bg-raised hover:text-fg" :title="s.wide ? 'Show the browse panes (\\)' : 'Hide the browse panes (\\)'" @click="s.wide = !s.wide">{{ s.wide ? '»' : '«' }}</button>
    <template v-for="(d, i) in s.docs" :key="d.id">
      <span v-if="marker === i" class="mb-0.5 h-6 w-0.5 shrink-0 rounded bg-focus" />
      <div
        role="tab" :aria-selected="i === s.active" :data-doc-index="i"
        class="group flex h-7 max-w-[220px] min-w-[90px] shrink-0 cursor-default items-center gap-1.5 rounded-t border border-b-0 pr-1 pl-2.5 text-[12px]"
        :class="[i === s.active ? 'border-line bg-panel text-fg' : 'border-transparent text-dim hover:bg-raised', s.docDrag?.from === pane && s.docDrag.index === i ? 'opacity-40' : '']"
        :title="guidOf(d) + '\nDrag to reorder or onto the other pane · right-click: move'"
        @mousedown.left="(e: MouseEvent) => down(e, i)" @auxclick.prevent="(e: MouseEvent) => e.button === 1 && closeDoc(i)" @mousedown.middle.prevent
        @contextmenu="(e: MouseEvent) => menu(e, i)"
      >
        <span class="h-2 w-2 shrink-0 rounded-[2px]" :style="{ background: guidOf(d) ? typeColor(guidType(guidOf(d))) : 'transparent' }" />
        <span class="min-w-0 flex-1 truncate">{{ guidOf(d) ? label(guidOf(d)) : 'empty' }}</span>
        <button class="rounded px-1 text-faint opacity-0 group-hover:opacity-100 hover:bg-line hover:text-fg" :class="i === s.active ? 'opacity-100' : ''" title="Close (Ctrl+W)" @mousedown.stop @click.stop="closeDoc(i)">×</button>
      </div>
    </template>
    <span v-if="marker >= 0 && marker >= s.docs.length" class="mb-0.5 h-6 w-0.5 shrink-0 rounded bg-focus" />
    <div class="h-full min-w-6 flex-1" />
    <div v-if="s.tabMenu && s.tabMenu.pane === pane" data-tab-menu class="fixed z-50 w-[230px] overflow-hidden rounded-md border border-line-strong bg-raised py-1 text-[12.5px] shadow-2xl shadow-black/50" :style="{ left: Math.min(s.tabMenu.x, 99999) + 'px', top: s.tabMenu.y + 'px' }" @mousedown.stop>
      <button class="menu-item" @click="moveDoc(pane, s.tabMenu!.index, 'other'); s.tabMenu = null">Move to {{ other }}</button>
      <button class="menu-item" @click="moveDocToWindow(pane, s.tabMenu!.index); s.tabMenu = null">Move to a new window</button>
      <div class="my-1 border-t border-line" />
      <button class="menu-item" @click="closeDoc(s.tabMenu!.index); s.tabMenu = null">Close</button>
    </div>
  </div>
</template>

<style scoped>
.menu-item { display: flex; width: 100%; padding: 4px 12px; text-align: left; }
.menu-item:hover { background: var(--color-sel); }
</style>
