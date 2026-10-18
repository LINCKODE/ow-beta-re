<script setup lang="ts">
// A drag handle between two panes. It resizes the pane on its `side` ('before' = the pane left of / above it grows
// with the drag; 'after' = the pane right of / below it). Double-click resets to the default. Sizes are kept per
// panel id in the app state (usePanel).
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ id: string; def: number; min?: number; max?: number; side?: 'before' | 'after'; vertical?: boolean }>()
const size = usePanel(p.id, p.def)
function down(e: MouseEvent) {
  if (e.button !== 0) return
  e.preventDefault()
  const start = p.vertical ? e.clientY : e.clientX
  const s0 = size.value
  const sign = p.side === 'after' ? -1 : 1
  const move = (ev: MouseEvent) => {
    const d = (p.vertical ? ev.clientY : ev.clientX) - start
    size.value = Math.round(Math.min(p.max ?? 2000, Math.max(p.min ?? 120, s0 + sign * d)))
  }
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    document.body.style.cursor = ''
    size.commit()
  }
  document.body.style.cursor = p.vertical ? 'row-resize' : 'col-resize'
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
}
function reset() { size.value = p.def; size.commit(true) }
</script>

<template>
  <div
    class="group relative z-10 shrink-0" :class="vertical ? 'h-0 w-full cursor-row-resize' : 'h-full w-0 cursor-col-resize'"
    role="separator" :aria-orientation="vertical ? 'horizontal' : 'vertical'" title="Drag to resize · double-click to reset"
    @mousedown="down" @dblclick="reset"
  >
    <div class="absolute bg-transparent transition-colors group-hover:bg-focus/50" :class="vertical ? 'inset-x-0 -top-[3px] h-[6px]' : 'inset-y-0 -left-[3px] w-[6px]'" />
  </div>
</template>
