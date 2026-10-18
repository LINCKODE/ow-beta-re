<script setup lang="ts">
// Hex dump, 16 bytes per row, fetched in 64 KB pages as you scroll.
import { reactive } from 'vue'
import { api } from '~/composables/api'

const p = defineProps<{ guid: string; size: number }>()
const PAGE = 65536
const pages = reactive(new Map<number, Uint8Array>())
const pending = new Set<number>()
const rows = Math.ceil(p.size / 16)

async function need(pg: number) {
  if (pages.has(pg) || pending.has(pg)) return
  pending.add(pg)
  const h = await api.Hex(p.guid, pg * PAGE, PAGE)
  const bin = atob(h.data ?? '')
  const a = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) a[i] = bin.charCodeAt(i)
  pages.set(pg, a)
}
function range(a: number, b: number) {
  for (let pg = Math.floor((a * 16) / PAGE); pg <= Math.floor((Math.max(a, b - 1) * 16) / PAGE); pg++) need(pg)
}
function row(i: number) {
  const off = i * 16, pg = Math.floor(off / PAGE), data = pages.get(pg)
  if (!data) return null
  const b = data.subarray(off - pg * PAGE, Math.min(off - pg * PAGE + 16, data.length))
  let hex = '', asc = ''
  for (let k = 0; k < 16; k++) {
    hex += k < b.length ? b[k].toString(16).padStart(2, '0') + (k === 7 ? '  ' : ' ') : '   '
    asc += k < b.length ? (b[k] >= 32 && b[k] < 127 ? String.fromCharCode(b[k]) : '·') : ''
  }
  return { off: off.toString(16).padStart(8, '0'), hex, asc }
}
</script>

<template>
  <VirtualList class="selectable h-full bg-well" :count="rows" :row-height="20" @range="range">
    <template #default="{ index }">
      <div class="mono flex h-full items-center gap-5 px-5 whitespace-pre">
        <template v-if="row(index)">
          <span class="text-faint">{{ row(index)!.off }}</span>
          <span class="text-fg">{{ row(index)!.hex }}</span>
          <span class="text-dim">{{ row(index)!.asc }}</span>
        </template>
      </div>
    </template>
  </VirtualList>
</template>
