<script setup lang="ts">
// Decoded STU document: instance table (left), the selected instance's field tree (right).
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ guid: string }>()
const { s } = useExplorer()
const doc = ref<any>(null)
const tree = ref<any>(null)
const err = ref('')
const filter = ref('')
const trail = ref<number[]>([])

onMounted(async () => {
  try {
    doc.value = await api.Document(p.guid)
    if (s.instance < 0) s.instance = doc.value.root
    else loadInstance(s.instance)
  } catch (e: any) { err.value = String(e) }
})
async function loadInstance(i: number) {
  if (i < 0) return
  tree.value = null
  try { tree.value = await api.Instance(p.guid, i) } catch (e: any) { err.value = String(e) }
}
watch(() => s.instance, loadInstance, { immediate: true })

function jump(i: number) {
  if (s.instance >= 0) trail.value = [...trail.value.slice(-12), s.instance]
  s.instance = i
}
function backInstance() {
  const i = trail.value.pop()
  if (i !== undefined) s.instance = i
}
const rows = computed(() => {
  const f = filter.value.trim().toLowerCase()
  const all = doc.value?.instances ?? []
  return f ? all.filter((x: any) => x.type.toLowerCase().includes(f) || String(x.index) === f || (x.summary ?? '').toLowerCase().includes(f)) : all
})
// the Ask panel follows the selected instance / field (useAsk liveSubject)
watch([() => s.instance, () => s.path], ([i, path]) => {
  if (i >= 0) s.extra.ask = { tab: 'tree', subject: { kind: 'field', guid: p.guid, instance: i, path: path || undefined, label: `${path ? 'field ' + path : 'instance ' + i} of ${p.guid}` } }
  else if (s.extra.ask?.tab === 'tree') delete s.extra.ask
}, { immediate: true })
onBeforeUnmount(() => { if (s.extra.ask?.tab === 'tree') delete s.extra.ask })
const selRow = computed(() => rows.value.findIndex((x: any) => x.index === s.instance))
const listW = usePanel('tree.list', 320)
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else class="flex h-full min-h-0">
    <div class="flex min-h-0 shrink-0 flex-col border-r border-line bg-well" :style="{ width: listW + 'px' }">
      <div class="flex items-center gap-2 border-b border-line px-3 py-1.5 text-[12px]">
        <span class="text-dim">{{ doc?.instances.length ?? 0 }} instances</span>
        <span v-if="doc" :class="doc.allCrcOk ? 'text-v-ok' : 'text-v-err'">{{ doc.allCrcOk ? 'every layout CRC matches' : 'layout CRC mismatch' }}</span>
      </div>
      <input v-model="filter" placeholder="Filter by type or index" class="m-2 rounded border border-line bg-panel px-2 py-1 placeholder:text-faint" />
      <VirtualList class="min-h-0 flex-1" :count="rows.length" :row-height="26" :selected="selRow" @select="(i: number) => jump(rows[i].index)">
        <template #default="{ index, selected }">
          <div class="flex h-full items-center gap-2 px-3" :class="selected ? 'bg-sel' : 'hover:bg-raised'" :title="rows[index].summary">
            <span class="mono w-9 shrink-0 text-right text-faint">{{ rows[index].index }}</span>
            <span class="min-w-0 flex-1 truncate" :class="rows[index].named ? '' : 'mono text-dim'">{{ rows[index].type }}</span>
            <span v-if="rows[index].root" class="text-[11px] text-focus">root</span>
            <span v-if="!rows[index].crcOk" class="text-[11px] text-v-err" title="layout CRC differs from the client registry">CRC</span>
            <span v-if="rows[index].errors" class="text-[11px] text-v-err">{{ rows[index].errors }} err</span>
          </div>
        </template>
      </VirtualList>
    </div>
    <Splitter id="tree.list" :def="320" :min="180" :max="900" />
    <div class="selectable min-h-0 min-w-0 flex-1 overflow-auto px-4 py-3">
      <div v-if="s.path || s.instance >= 0" class="mb-2 flex items-center gap-2 text-[12px]">
        <span class="text-dim">Selected</span><span class="mono min-w-0 truncate">{{ s.path || `instance ${s.instance}` }}</span>
        <OpenIn class="ml-auto" :ask="{ kind: 'field', guid: p.guid, instance: s.instance, path: s.path || undefined, label: `${s.path ? 'field ' + s.path : 'instance ' + s.instance} of ${p.guid}` }" />
      </div>
      <div v-if="trail.length" class="mb-2">
        <button class="text-[12px] text-dim hover:text-fg" @click="backInstance">Back to instance {{ trail[trail.length - 1] }}</button>
      </div>
      <TreeNode v-if="tree" :node="tree" :depth="0" :open-default="true" @jump="jump" />
    </div>
  </div>
</template>
