<script setup lang="ts">
// Middle pane: the resources matching the filter (type / package / search), paged from the backend.
import { computed, reactive, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { bytes, hex3, num } from '~/utils/format'
import { usePanel } from '~/composables/usePanel'

const { s, open } = useExplorer()
const CHUNK = 200
const rows = reactive(new Map<number, any>())
const pending = new Set<number>()
const total = ref(0)
const selected = ref(-1)
const list = ref<any>()
let gen = 0

const q = computed(() => ({ type: s.filter.type, package: s.filter.package, search: s.filter.search }))

async function fetchChunk(c: number, g: number) {
  if (pending.has(c)) return
  pending.add(c)
  const page = await api.List({ ...q.value, start: c * CHUNK, count: CHUNK })
  pending.delete(c)
  if (g !== gen) return
  total.value = page.total
  page.rows.forEach((r: any, k: number) => rows.set(page.start + k, r))
}
function reset() {
  gen++
  rows.clear()
  pending.clear()
  selected.value = -1
  list.value?.reset()
  fetchChunk(0, gen)
}
watch(q, reset, { deep: true })
watch(() => [s.listVersion, s.status?.open], () => { gen++; rows.clear(); pending.clear(); fetchChunk(0, gen) }, { immediate: true })

function range(a: number, b: number) {
  for (let c = Math.floor(a / CHUNK); c <= Math.floor(Math.max(a, b - 1) / CHUNK); c++) {
    if (!rows.has(c * CHUNK)) fetchChunk(c, gen)
  }
}
function select(i: number) {
  selected.value = i
  const r = rows.get(i)
  if (r) open(r.guid)
}
// Ctrl-click / middle-click a row: open it in a new tab.
function newTab(e: MouseEvent, i: number) {
  const r = rows.get(i)
  if (!r) return
  if (e.type === 'click' && e.shiftKey && !e.ctrlKey && !e.metaKey) { open(r.guid, { newWindow: true }); return }
  if ((e.type === 'click' && (e.ctrlKey || e.metaKey)) || (e.type === 'auxclick' && e.button === 1)) {
    e.preventDefault()
    open(r.guid, { newTab: true, background: !e.shiftKey || e.button === 1 })
  }
}
// "Reveal in list": the filter was set by reveal(); find the row and scroll to it.
watch(() => s.reveal?.n, async () => {
  const x = s.reveal
  if (!x) return
  await new Promise((r) => setTimeout(r, 50))
  const i = await api.IndexOf({ type: s.filter.type, package: s.filter.package, search: s.filter.search, start: 0, count: 0 }, x.guid)
  if (i >= 0) {
    selected.value = i
    list.value?.scrollTo(i)
    range(i, i + 40)
  }
})

const title = computed(() => {
  if (s.filter.package) return `Package ${s.filter.package}`
  if (s.filter.type >= 0) {
    const t = s.types.find((x: any) => x.type === s.filter.type)
    return `${hex3(s.filter.type)} ${t?.label ?? ''}`
  }
  return 'Everything'
})
// Textures (004), payloads (04D) and materials (008: a rendered sphere, T071) show a thumbnail (made in Go,
// /thumb/<guid>.png); "large" rows for browsing.
const isTex = (t: number) => t === 0x004 || t === 0x04d || t === 0x008
const large = usePanel('list.largeThumbs', 0)
const texList = computed(() => isTex(s.filter.type))
const rowH = computed(() => (texList.value && large.value ? 72 : 28))
function toggleLarge() { large.value = large.value ? 0 : 1; large.commit() }
watch(rowH, () => list.value?.reset())
defineExpose({ focus: () => list.value?.el?.focus() })
</script>

<template>
  <section class="flex min-h-0 flex-col border-r border-line bg-well">
    <header class="flex items-baseline gap-2 border-b border-line bg-panel px-3 py-2">
      <h2 class="min-w-0 truncate font-medium">{{ title }}</h2>
      <span v-if="s.filter.search" class="truncate text-dim">matching “{{ s.filter.search }}”</span>
      <button v-if="texList" class="ml-auto shrink-0 rounded px-1.5 text-[11px] hover:bg-raised hover:text-fg" :class="large ? 'text-fg' : 'text-dim'" title="Large thumbnails" @click="toggleLarge">▦ large</button>
      <span class="mono shrink-0 text-dim" :class="texList ? '' : 'ml-auto'">{{ num(total) }}</span>
    </header>
    <p v-if="s.filter.package" class="border-b border-line px-3 py-1.5 text-[12px] text-dim">
      Every resource this package lists (a resource can be in several packages).
    </p>
    <VirtualList ref="list" class="min-h-0 flex-1" :count="total" :row-height="rowH" :selected="selected" @select="select" @activate="select" @range="range">
      <template #default="{ index, selected: sel }">
        <div class="flex h-full items-center gap-3 px-3" :class="sel || rows.get(index)?.guid === s.current ? 'bg-sel' : 'hover:bg-raised'" @click="newTab($event, index)" @auxclick="newTab($event, index)">
          <template v-if="rows.get(index)">
            <img
              v-if="isTex(rows.get(index).type)" :src="`/thumb/${rows.get(index).guid}.png`" alt="" loading="lazy" decoding="async"
              class="shrink-0 rounded-sm bg-[#1f2738] object-contain" :class="rowH > 28 ? 'h-16 w-16' : 'h-5 w-5'"
              @error="($event.target as HTMLImageElement).style.visibility = 'hidden'"
            />
            <Guid :guid="rows.get(index).guid" :link="false" />
            <span class="min-w-0 flex-1 truncate" :class="rows.get(index).guess ? 'italic text-dim' : ''" :title="rows.get(index).note">{{ rows.get(index).name }}<span v-if="rows.get(index).note" class="ml-1.5 text-[11px] text-faint">{{ rows.get(index).note }}</span></span>
            <span class="mono shrink-0 text-faint">{{ bytes(rows.get(index).size) }}</span>
          </template>
          <span v-else class="mono text-faint">…</span>
        </div>
      </template>
    </VirtualList>
    <p v-if="total === 0 && s.status?.open" class="px-4 py-6 text-dim">
      Nothing matches. Search takes a GUID (any part of its 16 hex digits) or a name.
    </p>
  </section>
</template>
