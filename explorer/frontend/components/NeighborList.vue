<script setup lang="ts">
// One side of a resource in the reference index ("uses" / "used by"): kind and type filters with counts, a
// search, and a paged virtual list (fan-ins of thousands stay fast). In the dependency graph, rows can be
// added to the canvas (emit 'add').
import { computed, reactive, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { bytes, hex3, kindColor, num, typeColor } from '~/utils/format'

const p = defineProps<{ guid: string; dir: 'in' | 'out'; kinds?: string[]; types?: number[]; addable?: boolean; compact?: boolean; noMissing?: boolean }>()
const emit = defineEmits<{ (e: 'add', row: any): void }>()
const { s, open } = useExplorer()
const CHUNK = 150
const rows = reactive(new Map<number, any>())
const pending = new Set<number>()
const page = ref<any>(null)
const kinds = ref<string[]>(p.kinds ?? [])
const types = ref<number[]>(p.types ?? [])
const search = ref('')
const q = computed(() => ({ guid: p.guid, dir: p.dir, kinds: kinds.value, types: types.value, search: search.value.trim(), package: '', noMissing: !!p.noMissing }))
let gen = 0
const detail = ref<any>(null)

async function chunk(c: number, g: number) {
  if (pending.has(c)) return
  pending.add(c)
  try {
    const pg = await api.Neighbors({ ...q.value, start: c * CHUNK, count: CHUNK })
    if (g !== gen) return
    page.value = pg
    pg.rows.forEach((r: any, k: number) => rows.set(pg.start + k, r))
  } finally { pending.delete(c) }
}
let t: any
function reset() { gen++; rows.clear(); pending.clear(); chunk(0, gen) }
watch(() => [p.guid, p.dir, kinds.value, types.value, s.status?.index?.phase], reset, { deep: true, immediate: true })
watch(search, () => { clearTimeout(t); t = setTimeout(reset, 180) })
function range(a: number, b: number) {
  for (let c = Math.floor(a / CHUNK); c <= Math.floor(Math.max(a, b - 1) / CHUNK); c++) if (!rows.has(c * CHUNK)) chunk(c, gen)
}
function toggle<T>(list: { value: T[] }, x: T) {
  list.value = list.value.includes(x) ? list.value.filter((y) => y !== x) : [...list.value, x]
}
const kindLabel = (k: string) => s.kinds.find((x: any) => x.name === k)?.label ?? k
async function why(r: any) {
  if (detail.value?.guid === r.guid) { detail.value = null; return }
  const [from, to] = p.dir === 'out' ? [p.guid, r.guid] : [r.guid, p.guid]
  detail.value = { guid: r.guid, ...(await api.RefDetail(from, to)) }
}
function row(e: MouseEvent, r: any) {
  if (e.ctrlKey || e.metaKey) open(r.guid, { newTab: true, background: !e.shiftKey })
  else if (e.shiftKey) open(r.guid, { newWindow: true })
  else if (r.aux && r.type === 0x002 && p.dir === 'in') open(r.guid, { tab: 'map', instance: r.aux - 1 })
  else open(r.guid)
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div v-if="page && !page.ready" class="px-4 py-3 text-dim">The reference index is still being built (status bar)…</div>
    <template v-else-if="page">
      <div class="space-y-1.5 border-b border-line px-3 py-2 text-[11.5px]">
        <div class="flex flex-wrap gap-1">
          <button
            v-for="k in page.byKind" :key="k.kind" class="chip" :class="kinds.includes(k.kind) ? 'chip-on' : ''"
            :title="`${k.kind}: ${kinds.includes(k.kind) ? 'shown only' : 'click to show only this kind'}`" @click="toggle(kinds, k.kind)"
          ><span class="h-1.5 w-1.5 rounded-full" :style="{ background: kindColor(k.kind) }" />{{ k.label }} <span class="mono text-faint">{{ num(k.count) }}</span></button>
        </div>
        <div v-if="page.byType.length > 1 || types.length" class="flex flex-wrap gap-1">
          <button
            v-for="x in page.byType.slice(0, compact ? 8 : 18)" :key="x.type" class="chip" :class="types.includes(x.type) ? 'chip-on' : ''"
            :title="x.label" @click="toggle(types, x.type)"
          ><span class="mono" :style="{ color: typeColor(x.type) }">{{ hex3(x.type) }}</span><span class="max-w-[110px] truncate">{{ x.label }}</span> <span class="mono text-faint">{{ num(x.count) }}</span></button>
        </div>
        <input v-model="search" class="w-full rounded border border-line bg-well px-2 py-0.5 placeholder:text-faint" placeholder="Filter by GUID or name" />
      </div>
      <div class="flex items-baseline gap-2 px-3 py-1 text-[11.5px] text-dim">
        <span>{{ num(page.total) }}<template v-if="page.total !== page.all"> of {{ num(page.all) }}</template></span>
        <span class="ml-auto text-faint">Ctrl-click: new tab · “why” shows the field</span>
      </div>
      <VirtualList class="min-h-0 flex-1" :count="page.total" :row-height="compact ? 40 : 42" @range="range">
        <template #default="{ index }">
          <div v-if="rows.get(index)" class="group flex h-full cursor-default flex-col justify-center px-3 hover:bg-raised" @click="row($event, rows.get(index))" @auxclick="(e: MouseEvent) => e.button === 1 && open(rows.get(index).guid, { newTab: true, background: true })">
            <div class="flex items-baseline gap-2">
              <Guid :guid="rows.get(index).guid" :link="false" :missing="rows.get(index).missing" />
              <span class="min-w-0 flex-1 truncate" :class="rows.get(index).guess ? 'italic text-dim' : ''">{{ rows.get(index).name }}</span>
              <button v-if="addable" class="rounded px-1.5 text-[11px] text-focus opacity-0 group-hover:opacity-100 hover:bg-sel" title="Add to the graph" @click.stop="emit('add', rows.get(index))">+ graph</button>
              <button class="rounded px-1 text-[11px] text-faint opacity-0 group-hover:opacity-100 hover:text-fg" @click.stop="why(rows.get(index))">why</button>
              <span class="mono shrink-0 text-[11px] text-faint">{{ rows.get(index).missing ? 'not a resource' : bytes(rows.get(index).size) }}</span>
            </div>
            <div class="flex items-center gap-1.5 text-[11px] text-faint">
              <span v-for="k in rows.get(index).kinds" :key="k" class="flex items-center gap-1"><span class="h-1.5 w-1.5 rounded-full" :style="{ background: kindColor(k) }" />{{ kindLabel(k) }}</span>
              <span v-if="rows.get(index).aux" class="mono">record {{ rows.get(index).aux - 1 }}</span>
              <span v-if="rows.get(index).note" class="truncate">· {{ rows.get(index).note }}</span>
              <span class="mono ml-auto">{{ num(rows.get(index).in) }} in · {{ num(rows.get(index).out) }} out</span>
            </div>
          </div>
          <div v-else class="mono flex h-full items-center px-3 text-faint">…</div>
        </template>
      </VirtualList>
      <div v-if="detail" class="selectable max-h-[35%] overflow-auto border-t border-line bg-well px-3 py-2 text-[11.5px]">
        <div class="mb-1 flex items-baseline gap-2"><span class="text-dim">Why</span><Guid :guid="detail.guid" /><button class="ml-auto text-faint hover:text-fg" @click="detail = null">×</button></div>
        <p v-if="detail.aux" class="text-dim">map record {{ detail.aux - 1 }}</p>
        <p v-for="(x, i) in detail.paths" :key="i" class="mono break-all">{{ x }}</p>
        <p v-if="!detail.paths.length && !detail.aux" class="text-faint">{{ detail.kinds.map(kindLabel).join(', ') }}</p>
      </div>
    </template>
  </div>
</template>

<style scoped>
.chip { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--color-line); border-radius: 999px; padding: 0 8px; line-height: 20px; color: var(--color-dim); }
.chip:hover { color: var(--color-fg); border-color: var(--color-line-strong); }
.chip-on { background: var(--color-sel); color: var(--color-fg); border-color: var(--color-focus); }
</style>
