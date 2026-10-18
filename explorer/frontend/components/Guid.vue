<script setup lang="ts">
// A GUID: the type part (first 4 digits) in its type's hue, leading zeros of the index faint.
// Click opens it (link=false: the parent handles the click); Ctrl/⌘-click or middle-click opens it in a new
// tab; right-click shows the GUID menu (open, new tab, dependencies, copy, reveal).
import { computed } from 'vue'
import { guidType, typeColor } from '~/utils/format'
import { useExplorer } from '~/composables/useExplorer'

const p = defineProps<{ guid: string; link?: boolean; missing?: boolean; tab?: string; instance?: number }>()
const { open, s, pane } = useExplorer()
const g = computed(() => p.guid.padStart(16, '0'))
const t = computed(() => guidType(g.value))
const parts = computed(() => {
  const rest = g.value.slice(4)
  const i = rest.search(/[^0]/)
  const cut = i < 0 ? rest.length - 1 : i
  return { head: g.value.slice(0, 4), zeros: rest.slice(0, cut), tail: rest.slice(cut) }
})
const label = computed(() => s.types.find((x: any) => x.type === t.value)?.label ?? '')
const opts = computed(() => ({ tab: p.tab as any, instance: p.instance }))
function click(e: MouseEvent) {
  if (e.shiftKey && !e.ctrlKey && !e.metaKey) { e.stopPropagation(); e.preventDefault(); open(g.value, { ...opts.value, newWindow: true }); return }
  if (e.ctrlKey || e.metaKey) { e.stopPropagation(); e.preventDefault(); open(g.value, { ...opts.value, newTab: true, background: !e.shiftKey }); return }
  if (p.link === false) return
  e.stopPropagation()
  open(g.value, opts.value)
}
function aux(e: MouseEvent) {
  if (e.button !== 1) return
  e.stopPropagation(); e.preventDefault()
  open(g.value, { ...opts.value, newTab: true, background: true })
}
function menu(e: MouseEvent) {
  e.preventDefault(); e.stopPropagation()
  s.menu = { x: e.clientX, y: e.clientY, guid: g.value, pane }
}
</script>

<template>
  <component
    :is="link === false ? 'span' : 'button'"
    class="mono inline-flex items-baseline whitespace-nowrap rounded-sm px-0.5 -mx-0.5"
    :class="[link === false ? '' : 'hover:bg-raised hover:underline decoration-dotted underline-offset-2', missing ? 'opacity-45' : '']"
    :title="`ow://${g} · type ${t.toString(16).toUpperCase().padStart(3, '0')} ${label}${missing ? ' · not in this install' : ''}\nClick: open · Ctrl/middle-click: new tab · Shift-click: new window · right-click: more`"
    @click="click" @auxclick="aux" @mousedown.middle.prevent @contextmenu="menu"
  >
    <span :style="{ color: typeColor(t) }">{{ parts.head }}</span><span class="text-faint">{{ parts.zeros }}</span><span class="text-fg">{{ parts.tail }}</span>
  </component>
</template>
