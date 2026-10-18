<script setup lang="ts">
// Material (0x008): a live preview (MaterialPreview, T071) next to the shader family, the preview's slot → input
// mapping with its confidence, the texture bindings (slot hash → name when known, format, size, thumbnail), the
// parameter stream and the constant buffers (docs/AssetFormats.md §3, docs/Explorer.md "Material view").
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ guid: string }>()
const { s: ex } = useExplorer()
const v = ref<any>(null)
const err = ref('')
const sideW = usePanel('material.side', 440)
onMounted(async () => {
  try {
    v.value = await api.Material(p.guid)
    ex.extra.ask = { tab: 'material', subject: { kind: 'material', guid: v.value.guid, label: `material ${v.value.guid}` } }
  } catch (e: any) { err.value = String(e) }
})
onBeforeUnmount(() => { if (ex.extra.ask?.tab === 'material') delete ex.extra.ask })
const fl = (x: number) => (Math.abs(x) >= 1e5 || (x !== 0 && Math.abs(x) < 1e-3) ? x.toExponential(2) : +x.toFixed(4))
const hex3 = (n: number) => '0x' + n.toString(16).toUpperCase().padStart(2, '0')
const label: Record<string, string> = { color: 'colour', opacity: 'opacity', normal: 'normal', roughness: 'roughness', specular: 'specular', metalness: 'metalness', ao: 'AO', emissive: 'emissive', colorB: 'layer B', height: 'blend height', overlay: 'overlay' }
</script>

<template>
  <div class="flex h-full min-h-0">
    <div class="flex min-w-0 flex-1 flex-col">
      <div v-if="err" class="m-5 rounded border border-v-err/40 bg-v-err/10 p-3 text-[12.5px]">
        <b class="text-v-err">This material can't be shown.</b>
        <div class="mono mt-1 text-dim">{{ err }}</div>
      </div>
      <MaterialPreview v-else-if="v" :v="v" />
      <p v-else class="p-5 text-dim">loading…</p>
    </div>
    <template v-if="v">
      <Splitter id="material.side" :def="440" :min="280" :max="900" side="after" />
      <aside class="selectable min-h-0 shrink-0 overflow-auto border-l border-line p-4 text-[12.5px]" :style="{ width: sideW + 'px' }">
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt class="text-dim">Shader</dt><dd><Guid :guid="v.shader" /> <span class="text-faint">(0x088)</span></dd>
          <dt class="text-dim">Family</dt>
          <dd>{{ hex3(v.preview.family.id) }} · {{ v.preview.family.name }} <span class="text-faint">[{{ v.preview.family.conf }}] · {{ v.preview.family.mode }}</span>
            <div v-if="v.preview.family.note" class="text-[11.5px] text-dim">{{ v.preview.family.note }}</div></dd>
          <dt class="text-dim">+0x08</dt><dd><Guid v-if="v.ref" :guid="v.ref" /><span v-else class="text-faint">none</span></dd>
          <dt class="text-dim">+0x40</dt><dd class="mono">{{ v.color.map(fl).join(', ') }}</dd>
          <dt class="text-dim">+0x68</dt><dd><span class="mono">0x{{ v.flags68.toString(16) }}</span><span v-if="v.helper" class="ml-2 rounded bg-v-err/20 px-1 text-v-err">helper / editor material (bit 1): not drawn in game [?]</span></dd>
        </dl>
        <ul v-if="v.preview.notes.length" class="mt-3 list-disc pl-5 text-[11.5px] text-dim">
          <li v-for="(n, i) in v.preview.notes" :key="i">{{ n }}</li>
        </ul>

        <h4 class="mt-5 mb-1 text-dim">Preview inputs</h4>
        <p v-if="!v.preview.inputs.length" class="text-faint">none: a plain surface</p>
        <table v-else class="w-full">
          <thead class="text-left text-[11.5px] text-faint"><tr><th>input</th><th /><th>slot</th><th>channel</th><th>conf</th></tr></thead>
          <tbody>
            <tr v-for="(i, k) in v.preview.inputs" :key="k" class="border-t border-line/50 align-top" :title="i.note">
              <td class="py-1 pr-2">{{ label[i.input] }}<span v-if="i.uvSet" class="ml-1 text-[11px] text-faint">UV{{ i.uvSet }}</span><span v-if="i.intensity !== undefined" class="ml-1 text-[11px] text-faint">×{{ fl(i.intensity) }}</span></td>
              <td class="py-1 pr-2"><img :src="`/thumb/${i.texture}.png`" class="h-8 w-8 rounded-sm object-cover" loading="lazy"></td>
              <td class="pr-2"><span :class="i.conf === 'D' ? '' : 'text-dim'">{{ i.slotName || '—' }}</span> <span class="mono text-[11px] text-faint">{{ i.slot }}</span>
                <div v-if="i.note" class="text-[11px] text-faint">{{ i.note }}</div></td>
              <td class="mono pr-2">{{ i.channel }}</td>
              <td :class="i.conf === 'D' ? 'text-v-ok' : 'text-dim'">{{ i.conf }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="v.preview.unmapped.length" class="mt-2 text-[11.5px] text-dim">
          Not used by the preview:
          <span v-for="(u, k) in v.preview.unmapped" :key="k" class="mr-2 inline-block"><span class="mono">{{ u.slot }}</span> {{ u.slotName }}<span class="text-faint"> {{ u.format }}</span></span>
        </div>
        <p class="mt-1 text-[11px] text-faint">D: read in the shader bytecode or the data; ?: inferred. "~" slot names come from their use in the bytecode, not from a recovered CRC. docs/Explorer.md "Material view".</p>

        <h4 class="mt-5 mb-1 text-dim">Textures ({{ v.textures.length }})</h4>
        <p v-if="!v.textures.length" class="text-faint">none</p>
        <table v-else class="w-full">
          <thead class="text-left text-[11.5px] text-faint"><tr><th /><th>slot</th><th>texture</th><th>format</th><th>size</th><th>flags</th></tr></thead>
          <tbody>
            <tr v-for="(t, i) in v.textures" :key="i" class="border-t border-line/50">
              <td class="py-1 pr-2"><img v-if="!t.missing" :src="`/thumb/${t.guid}.png`" class="h-12 w-12 rounded-sm object-cover" loading="lazy"></td>
              <td class="pr-3"><span v-if="t.slotName" :class="t.slotName.endsWith('?') || t.slotName.endsWith('~') ? 'text-dim' : ''">{{ t.slotName }}</span> <span class="mono text-[11px] text-faint">{{ t.slot }}</span></td>
              <td class="pr-3"><Guid :guid="t.guid" /><span v-if="t.missing" class="ml-1 text-v-err">missing</span></td>
              <td class="pr-3">{{ t.format }}</td>
              <td class="pr-3">{{ t.width }}×{{ t.height }}</td>
              <td>{{ t.flags }}</td>
            </tr>
          </tbody>
        </table>
        <p class="mt-1 text-[11.5px] text-faint">Slot names: CRC32 of the lower-cased name when recovered; "?" inferred from the formats bound; "~" named by their use in the bytecode.</p>

        <h4 class="mt-5 mb-1 text-dim">Parameters ({{ v.params.length }})</h4>
        <p v-if="!v.params.length" class="text-faint">none</p>
        <div v-for="(q, i) in v.params" :key="i" class="flex gap-3 border-t border-line/50 py-0.5">
          <span class="mono text-faint">{{ q.hash }}</span><span class="mono">{{ q.values.map(fl).join(', ') }}</span><span class="ml-auto text-dim">{{ q.use }}</span>
        </div>

        <h4 class="mt-5 mb-1 text-dim">Constant buffers ({{ v.buffers.length }})</h4>
        <p v-if="!v.buffers.length" class="text-faint">none</p>
        <div v-for="(b, i) in v.buffers" :key="i" class="border-t border-line/50 py-1">
          <div>hash <span class="mono">{{ b.hash }}</span> · {{ b.size }} bytes</div>
          <div class="mono text-[11px] leading-relaxed text-dim">
            <span v-for="(q, k) in Math.ceil(b.floats.length / 4)" :key="k" class="mr-3 inline-block">[{{ b.floats.slice(k * 4, k * 4 + 4).map(fl).join(' ') }}]</span>
          </div>
        </div>
      </aside>
    </template>
  </div>
</template>
