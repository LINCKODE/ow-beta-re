<script setup lang="ts">
// Info: what every resource gets, whatever its format: type and its size statistics, decoder status,
// package detail, references in / out by kind and by type (click to open the graph or the list).
import { computed, onMounted, ref, watch } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { bytes, hex3, kindColor, num, typeColor } from '~/utils/format'

const p = defineProps<{ guid: string }>()
const { s } = useExplorer()
const v = ref<any>(null)
const err = ref('')
async function load() {
  try { v.value = await api.Info(p.guid) } catch (e: any) { err.value = String(e) }
}
onMounted(load)
watch(() => s.status?.index?.phase, (ph) => { if (ph === 'done' && v.value && !v.value.ready) load() })
const statusColor = computed(() => ({ ok: 'text-v-ok', error: 'text-v-err', none: 'text-dim' } as any)[v.value?.decoder.status] ?? '')
const r = computed(() => s.resource)
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else-if="v" class="selectable h-full overflow-auto px-5 py-4">
    <div class="grid max-w-[1100px] grid-cols-[repeat(auto-fit,minmax(300px,1fr))] gap-4">
      <section class="card">
        <h3>Format</h3>
        <dl>
          <dt>Decoder</dt>
          <dd><span :class="statusColor">{{ v.decoder.name || 'none' }}</span></dd>
          <dt>Status</dt>
          <dd :class="statusColor">{{ v.decoder.status }}<span v-if="v.decoder.detail" class="text-dim"> · {{ v.decoder.detail }}</span></dd>
          <template v-if="v.decoder.planned"><dt>Planned</dt><dd>{{ v.decoder.planned }}</dd></template>
          <dt>Kind</dt><dd>{{ r?.kind }} <span class="mono text-faint">magic “{{ r?.magic }}”</span></dd>
          <dt>Flags</dt><dd class="mono">0x{{ (r?.flags ?? 0).toString(16).padStart(8, '0') }}</dd>
        </dl>
      </section>

      <section class="card">
        <h3>Type <span class="mono" :style="{ color: typeColor(v.stats.type) }">{{ hex3(v.stats.type) }}</span> {{ v.stats.label }}</h3>
        <dl>
          <dt>Label from</dt><dd>{{ v.stats.source || 'no label: unknown format' }}</dd>
          <dt>Resources</dt><dd class="mono">{{ num(v.stats.count) }} <span class="text-faint">({{ bytes(v.stats.bytes) }})</span></dd>
          <dt>Sizes</dt>
          <dd class="mono">{{ bytes(v.stats.min) }} · median {{ bytes(v.stats.median) }} · max {{ bytes(v.stats.max) }}</dd>
          <dt>STU</dt><dd>{{ v.stats.stu ? 'yes (STUD files)' : 'no' }}</dd>
        </dl>
        <button class="mt-2 text-[12px] text-focus hover:underline" @click="s.filter.package = ''; s.filter.search = ''; s.filter.type = v.stats.type; s.rail = 'types'">List every {{ hex3(v.stats.type) }}</button>
      </section>

      <section v-if="v.package" class="card">
        <h3>Asset package</h3>
        <dl>
          <dt>Records</dt><dd class="mono">{{ num(v.package.records) }}</dd>
          <dt>Dependencies</dt><dd class="mono">{{ num(v.package.deps) }} packages</dd>
          <dt>Bundle</dt><dd><Guid v-if="v.package.bundle" :guid="v.package.bundle" /><span v-else class="text-faint">none</span></dd>
          <dt>Owner (APM)</dt><dd><Guid v-if="v.package.owner" :guid="v.package.owner" /></dd>
          <dt>Manifest</dt><dd class="mono break-all text-dim">{{ v.package.manifest }}</dd>
        </dl>
        <button class="mt-2 text-[12px] text-focus hover:underline" @click="s.filter.type = -1; s.filter.search = ''; s.filter.package = p.guid; s.rail = 'packages'">List its resources</button>
      </section>

      <section v-for="side in (['out', 'in'] as const)" :key="side" class="card">
        <h3>{{ side === 'out' ? 'Uses' : 'Used by' }} <span class="mono text-faint">{{ num(v[side].all) }}</span></h3>
        <p v-if="!v.ready" class="text-dim">The reference index is still being built.</p>
        <p v-else-if="!v[side].all" class="text-faint">{{ side === 'out' ? 'References nothing the index knows.' : 'Nothing refers to it.' }}</p>
        <template v-else>
          <div class="mb-2 space-y-0.5">
            <div v-for="k in v[side].byKind" :key="k.kind" class="flex items-center gap-2 text-[12px]">
              <span class="h-2 w-2 rounded-full" :style="{ background: kindColor(k.kind) }" /><span class="flex-1">{{ k.label }}</span><span class="mono text-dim">{{ num(k.count) }}</span>
            </div>
          </div>
          <div class="space-y-0.5 border-t border-line pt-2">
            <div v-for="t in v[side].byType.slice(0, 12)" :key="t.type" class="flex items-center gap-2 text-[12px]">
              <span class="mono w-8" :style="{ color: typeColor(t.type) }">{{ hex3(t.type) }}</span><span class="min-w-0 flex-1 truncate">{{ t.label }}</span><span class="mono text-dim">{{ num(t.count) }}</span>
            </div>
            <p v-if="v[side].byType.length > 12" class="text-[11px] text-faint">{{ v[side].byType.length - 12 }} more types</p>
          </div>
          <div class="mt-2 flex gap-3 text-[12px]">
            <button class="text-focus hover:underline" @click="s.tab = 'deps'">Graph</button>
            <button class="text-focus hover:underline" @click="s.tab = 'refs'">List</button>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
.card { border: 1px solid var(--color-line); border-radius: 8px; padding: 12px 14px; background: color-mix(in oklab, var(--color-well) 60%, transparent); }
.card h3 { font-weight: 500; margin-bottom: 8px; }
.card dl { display: grid; grid-template-columns: auto 1fr; gap: 3px 14px; font-size: 12px; }
.card dt { color: var(--color-dim); }
</style>
