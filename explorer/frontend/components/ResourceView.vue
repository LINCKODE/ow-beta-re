<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { tabsFor, useExplorer, type Tab } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { askAbout } from '~/composables/useAsk'
import { bytes, hex3, num, typeColor } from '~/utils/format'

const { s, pane, open, togglePin, copy, reveal, goto, label, closeSplit } = useExplorer()
const inSplit = computed(() => s.split === pane)
const r = computed(() => s.resource)
const tabs = computed(() => tabsFor(r.value))
const names: Record<Tab, string> = {
  info: 'Info', map: 'Map', texture: 'Texture', model: 'Model', material: 'Material', audio: 'Audio', tree: 'Tree', graph: 'Graph', entity: 'Components', deps: 'Dependencies', refs: 'References',
  hex: 'Hex', text: 'Text',
}
const exported = ref('')
async function exportAs(kind: string) {
  exported.value = ''
  try {
    const p = await api.Export(r.value.guid, kind)
    if (p) exported.value = `Saved ${p}`
  } catch (e: any) { exported.value = `Export failed: ${e}` }
}
// Breadcrumbs: the active tab's history up to the current entry (the last 6).
const crumbs = computed(() => {
  const out = s.history.slice(0, s.cursor + 1).map((g, k) => ({ g, k }))
  return out.slice(-6)
})
const pkgNames = ref<Record<string, string>>({})
async function loadPkgNames() {
  if (!r.value?.packages?.length) return
  const rows = await api.Rows(r.value.packages)
  for (const x of rows) if (x.name) pkgNames.value[x.guid] = x.name
}
watch(() => [r.value?.guid, s.listVersion], loadPkgNames, { immediate: true })
</script>

<template>
  <section class="flex min-h-0 min-w-0 flex-col bg-panel">
    <DocTabs />
    <div v-if="s.error" class="p-8 text-v-err">{{ s.error }}</div>
    <div v-else-if="!r" class="flex flex-1 items-center justify-center p-10 text-center text-dim">
      <div>
        <p class="text-[15px] text-fg">Pick a resource to inspect it.</p>
        <p class="mt-2">Browse by type on the left, or press <kbd class="mono rounded border border-line px-1">/</kbd> and type a GUID or a name.</p>
        <p class="mt-1">Every GUID is a link: Ctrl- or middle-click opens it in a new tab, right-click for more.</p>
      </div>
    </div>
    <template v-else>
      <header class="border-b border-line px-5 pt-3">
        <nav v-if="crumbs.length > 1" class="mb-1.5 flex min-w-0 items-center gap-1 overflow-hidden text-[12px] text-dim" aria-label="Breadcrumbs">
          <template v-for="(c, i) in crumbs" :key="c.k">
            <span v-if="i" class="text-faint">›</span>
            <button
              class="max-w-[200px] shrink truncate rounded px-1 hover:bg-raised hover:text-fg" :class="c.k === s.cursor ? 'text-fg' : ''"
              :title="c.g" @click="goto(c.k)"
            >{{ label(c.g) }}</button>
          </template>
        </nav>
        <div class="flex items-start gap-4">
          <div class="min-w-0 flex-1 overflow-hidden">
            <div class="flex items-baseline gap-2 text-[18px] leading-tight">
              <Guid :guid="r.guid" :link="false" class="!text-[18px]" />
              <button class="rounded px-1 text-[11px] text-faint hover:bg-raised hover:text-fg" title="Copy GUID" @click="copy(r.guid, 'GUID copied')">copy</button>
            </div>
            <p class="mt-1 flex min-w-0 items-baseline gap-2 text-[15px]">
              <span class="truncate" :class="r.name.guess ? 'italic text-dim' : ''" :title="r.name.source">{{ r.name.text || '' }}</span>
              <span v-if="r.name.note" class="shrink-0 rounded bg-raised px-1.5 text-[11.5px] text-dim">{{ r.name.note }}</span>
            </p>
          </div>
          <div class="flex max-w-[60%] flex-wrap items-center justify-end gap-1">
            <button class="rounded px-2.5 py-1 hover:bg-raised" :class="r.pinned ? 'text-focus' : 'text-dim'" title="Pin (P)" @click="togglePin">
              {{ r.pinned ? 'Pinned' : 'Pin' }}
            </button>
            <button class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" title="Show it in the type list" @click="reveal(r.guid, 'type')">Reveal</button>
            <button class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" title="Open it in a new window (Shift-click on a GUID)" @click="open(r.guid, { newWindow: true, tab: s.tab, instance: s.instance })">New window</button>
            <button v-if="!inSplit" class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" title="Keep it open in a second pane on the right while this one navigates" @click="open(r.guid, { split: true, tab: s.tab, instance: s.instance })">Split</button>
            <button v-else class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" title="Close the split pane" @click="closeSplit">Close pane</button>
            <button class="rounded px-2.5 py-1 text-focus hover:bg-raised" title="Ask the Claude session that watches the explorer about this resource" @click="askAbout({ kind: 'resource', guid: r.guid, label: label(r.guid) })">✦ Ask</button>
            <button class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" @click="exportAs('bytes')">Export bytes</button>
            <button v-if="r.kind === 'stu'" class="rounded px-2.5 py-1 text-dim hover:bg-raised hover:text-fg" @click="exportAs('json')">Export JSON</button>
          </div>
        </div>
        <dl class="mt-2 grid grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-1 text-[12px]">
          <dt class="text-dim">Type</dt>
          <dd><span class="mono" :style="{ color: typeColor(r.type) }">{{ hex3(r.type) }}</span> {{ r.typeLabel }}</dd>
          <dt class="text-dim">Size</dt>
          <dd class="mono">{{ num(r.size) }} <span class="text-faint">({{ bytes(r.size) }})</span></dd>
          <dt class="text-dim">Content key</dt>
          <dd class="mono selectable truncate text-dim">{{ r.ckey }}</dd>
          <dt class="text-dim">References</dt>
          <dd>
            <button class="hover:underline" @click="s.tab = 'deps'"><span class="mono">{{ num(r.refsOut) }}</span> out, <span class="mono">{{ num(r.refsIn) }}</span> in</button>
            <span class="ml-2 mono text-faint">magic “{{ r.magic }}”</span>
          </dd>
          <dt class="text-dim">Packages</dt>
          <dd class="col-span-3 flex flex-wrap gap-x-3">
            <span v-for="p in r.packages" :key="p" class="inline-flex items-baseline gap-1">
              <Guid :guid="p" />
              <span v-if="pkgNames[p]" class="text-faint">{{ pkgNames[p].replace(/^Package of /, '') }}</span>
              <button class="text-[11px] text-faint hover:text-fg" title="List this package's resources" @click="reveal(r.guid, 'package', p)">list</button>
            </span>
            <span v-if="!r.packages.length" class="text-faint">none (a package itself, or a bundle)</span>
          </dd>
        </dl>
        <p v-if="exported" class="mt-2 text-[12px] text-dim">{{ exported }}</p>
        <div class="mt-2 flex gap-2" role="tablist">
          <button v-for="t in tabs" :key="t" class="tab" role="tab" :aria-selected="s.tab === t" @click="s.tab = t">
            {{ names[t] }}<span v-if="t === 'refs' && r.refsIn" class="ml-1 text-faint">{{ num(r.refsIn) }} in</span>
          </button>
        </div>
      </header>
      <div class="min-h-0 flex-1">
        <InfoView v-if="s.tab === 'info'" :key="'i' + r.guid" :guid="r.guid" />
        <MapView v-else-if="s.tab === 'map'" :key="'m' + r.guid" :guid="r.guid" />
        <TextureView v-else-if="s.tab === 'texture'" :key="'x' + r.guid" :guid="r.guid" />
        <ModelView v-else-if="s.tab === 'model'" :key="'o' + r.guid" :guid="r.guid" />
        <MaterialView v-else-if="s.tab === 'material'" :key="'a' + r.guid" :guid="r.guid" />
        <AudioView v-else-if="s.tab === 'audio'" :key="'u' + r.guid" :guid="r.guid" />
        <GraphView v-else-if="s.tab === 'graph'" :key="'g' + r.guid" :guid="r.guid" />
        <EntityView v-else-if="s.tab === 'entity'" :key="'e' + r.guid" :guid="r.guid" />
        <StuTree v-else-if="s.tab === 'tree'" :key="'t' + r.guid" :guid="r.guid" />
        <DepGraph v-else-if="s.tab === 'deps'" :key="'d' + r.guid" :guid="r.guid" />
        <RefsView v-else-if="s.tab === 'refs'" :key="'r' + r.guid" :guid="r.guid" />
        <HexView v-else-if="s.tab === 'hex'" :key="'h' + r.guid" :guid="r.guid" :size="r.size" />
        <div v-else-if="s.tab === 'text'" class="selectable h-full overflow-auto p-6 text-[15px] leading-relaxed whitespace-pre-wrap">{{ r.text }}</div>
      </div>
    </template>
  </section>
</template>
