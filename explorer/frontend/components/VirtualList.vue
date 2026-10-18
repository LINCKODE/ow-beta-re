<script setup lang="ts" generic="T">
// Fixed-row-height virtual list with keyboard navigation (↑ ↓ PgUp PgDn Home End, Enter).
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'

const p = defineProps<{ count: number; rowHeight: number; selected?: number }>()
const emit = defineEmits<{ (e: 'select', i: number): void; (e: 'activate', i: number): void; (e: 'range', a: number, b: number): void }>()
const el = ref<HTMLElement>()
const top = ref(0)
const height = ref(400)
const first = computed(() => Math.max(0, Math.floor(top.value / p.rowHeight) - 8))
const last = computed(() => Math.min(p.count, Math.ceil((top.value + height.value) / p.rowHeight) + 8))
const indices = computed(() => Array.from({ length: Math.max(0, last.value - first.value) }, (_, k) => first.value + k))

let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(() => { height.value = el.value?.clientHeight ?? 400 })
  ro.observe(el.value!)
})
onBeforeUnmount(() => ro?.disconnect())
watch([first, last], ([a, b]) => emit('range', a, b), { immediate: true })

function scrollTo(i: number) {
  const e = el.value
  if (!e) return
  const y = i * p.rowHeight
  if (y < e.scrollTop) e.scrollTop = y
  else if (y + p.rowHeight > e.scrollTop + e.clientHeight) e.scrollTop = y + p.rowHeight - e.clientHeight
}
watch(() => p.selected, (i) => { if (i !== undefined && i >= 0) scrollTo(i) })

function key(ev: KeyboardEvent) {
  const page = Math.max(1, Math.floor(height.value / p.rowHeight) - 1)
  const cur = p.selected ?? -1
  const go = (i: number) => { ev.preventDefault(); emit('select', Math.max(0, Math.min(p.count - 1, i))) }
  switch (ev.key) {
    case 'ArrowDown': return go(cur + 1)
    case 'ArrowUp': return go(cur - 1)
    case 'PageDown': return go(cur + page)
    case 'PageUp': return go(cur - page)
    case 'Home': return go(0)
    case 'End': return go(p.count - 1)
    case 'Enter': if (cur >= 0) { ev.preventDefault(); emit('activate', cur) }
  }
}
defineExpose({ el, scrollTo, reset: () => { if (el.value) el.value.scrollTop = 0 } })
</script>

<template>
  <div ref="el" class="relative overflow-auto outline-none" tabindex="0" @scroll="top = ($event.target as HTMLElement).scrollTop" @keydown="key">
    <div :style="{ height: count * rowHeight + 'px' }" class="relative">
      <div
        v-for="i in indices" :key="i"
        class="absolute inset-x-0"
        :style="{ top: i * rowHeight + 'px', height: rowHeight + 'px' }"
        @mousedown="(e: MouseEvent) => { if (e.button === 0 && !e.ctrlKey && !e.metaKey && !e.shiftKey) emit('select', i) }" @dblclick="emit('activate', i)"
      >
        <slot :index="i" :selected="i === selected" />
      </div>
    </div>
  </div>
</template>
