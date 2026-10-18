<script setup lang="ts">
// Left rail: what to browse (types, packages, pinned, recent).
import { computed, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { bytes, hex3, num } from '~/utils/format'

const { s, open } = useExplorer()
const sections = [
  { id: 'types', label: 'Types' },
  { id: 'packages', label: 'Packages' },
  { id: 'pinned', label: 'Pinned' },
  { id: 'recent', label: 'Recent' },
] as const

const typeSel = computed(() => s.filter.type)
function pickType(t: number) {
  s.filter.package = ''
  s.filter.type = t
}
const pkgFilter = ref('')
const pkgs = computed(() => {
  const q = pkgFilter.value.trim().toLowerCase().replace(/^0x/, '')
  const t = pkgFilter.value.trim().toLowerCase()
  return q ? s.packages.filter((p: any) => p.guid.includes(q) || (p.name ?? '').toLowerCase().includes(t)) : s.packages
})
const pkgSel = computed(() => pkgs.value.findIndex((p: any) => p.guid === s.filter.package))
function pickPkg(i: number) {
  const p = pkgs.value[i]
  if (!p) return
  s.filter.type = -1
  s.filter.package = p.guid
}

const saved = ref<any[]>([])
async function loadSaved() {
  if (s.rail === 'pinned') saved.value = await api.Rows(s.ui.pinned)
  if (s.rail === 'recent') saved.value = await api.Rows(s.ui.recent)
}
watch(() => [s.rail, s.ui.pinned, s.ui.recent, s.status?.open], loadSaved, { immediate: true })
const savedSel = ref(-1)
</script>

<template>
  <nav class="flex min-h-0 flex-col border-r border-line bg-panel">
    <div class="flex border-b border-line px-1 [&_.tab]:px-2" role="tablist">
      <button v-for="x in sections" :key="x.id" class="tab" role="tab" :aria-selected="s.rail === x.id" @click="s.rail = x.id">
        {{ x.label }}<span v-if="x.id === 'pinned' && s.ui.pinned.length" class="ml-1 text-faint">{{ s.ui.pinned.length }}</span>
      </button>
    </div>

    <div v-if="s.rail === 'types'" class="min-h-0 flex-1 overflow-auto py-1">
      <button
        class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left hover:bg-raised"
        :class="typeSel === -1 && !s.filter.package ? 'bg-sel' : ''" @click="pickType(-1)"
      >
        <span class="h-2.5 w-2.5 rounded-[3px] border border-line-strong" />
        <span class="flex-1">Everything</span>
        <span class="mono text-dim">{{ num(s.status?.resources ?? 0) }}</span>
      </button>
      <button
        v-for="t in s.types" :key="t.type"
        class="group flex w-full items-center gap-2.5 px-3 py-[5px] text-left hover:bg-raised"
        :class="typeSel === t.type ? 'bg-sel' : ''"
        :title="`${t.label} — ${t.source || 'no label'} · ${bytes(t.bytes)}`"
        @click="pickType(t.type)"
      >
        <TypeSwatch :type="t.type" />
        <span class="mono w-8 text-dim">{{ hex3(t.type) }}</span>
        <span class="min-w-0 flex-1 truncate" :class="t.label === 'unknown' ? 'text-faint italic' : ''">{{ t.label }}</span>
        <span class="mono text-dim">{{ num(t.count) }}</span>
      </button>
    </div>

    <div v-else-if="s.rail === 'packages'" class="flex min-h-0 flex-1 flex-col">
      <input v-model="pkgFilter" placeholder="Filter packages by GUID or owner name" class="mono m-2 rounded border border-line bg-well px-2 py-1 text-fg placeholder:text-faint" />
      <VirtualList class="min-h-0 flex-1" :count="pkgs.length" :row-height="30" :selected="pkgSel" @select="pickPkg">
        <template #default="{ index, selected }">
          <div class="flex h-full items-center gap-2 px-3" :class="selected ? 'bg-sel' : 'hover:bg-raised'">
            <Guid :guid="pkgs[index].guid" :link="false" />
            <span class="min-w-0 flex-1 truncate text-dim" :title="pkgs[index].manifest">{{ (pkgs[index].name ?? '').replace(/^Package of /, '') }}<span v-if="pkgs[index].manifest.includes('speech')" class="ml-1 text-faint">speech</span></span>
            <span class="mono ml-auto text-dim">{{ num(pkgs[index].records) }}</span>
          </div>
        </template>
      </VirtualList>
    </div>

    <div v-else class="min-h-0 flex-1 overflow-auto py-1">
      <p v-if="!saved.length" class="px-4 py-6 text-dim">
        {{ s.rail === 'pinned' ? 'Pin a resource with P or the pin button to keep it here.' : 'Resources you open appear here.' }}
      </p>
      <button
        v-for="(r, i) in saved" :key="r.guid"
        class="flex w-full flex-col items-start px-3 py-1.5 text-left hover:bg-raised" :class="r.guid === s.current ? 'bg-sel' : ''"
        @click="open(r.guid)"
      >
        <Guid :guid="r.guid" :link="false" />
        <span v-if="r.name" class="w-full truncate text-dim">{{ r.name }}</span>
      </button>
    </div>
  </nav>
</template>
