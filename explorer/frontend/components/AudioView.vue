<script setup lang="ts">
// Audio (T065, docs/AssetFormats.md §5, docs/Explorer.md "Audio view"):
//   WEM 03F / 0B2: the player, header, chunks, who lists / plays it;
//   sound 02C: its WEMs (click to play), the play / stop event trees from its bank;
//   bank 043: header, object counts, event trees, every HIRC object, the switch / state / parameter ids it holds;
//   switch group, switch, parameter, state group 02D–030: name, Wwise id (FNV-1 check), switches, banks using it.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '~/composables/api'
import { useExplorer } from '~/composables/useExplorer'
import { usePanel } from '~/composables/usePanel'

const p = defineProps<{ guid: string }>()
const { s: ex, flash } = useExplorer()
const v = ref<any>(null)
const err = ref('')
const sel = ref('') // the WEM in the player
const autoplay = ref(false)
const filter = ref('')
const sideW = usePanel('audio.side', 460)

onMounted(async () => {
  try {
    v.value = await api.Audio(p.guid)
    if (v.value.kind === 'wem') sel.value = v.value.wem.guid
    else if (v.value.kind === 'sound' && v.value.sound.files.length) sel.value = v.value.sound.files[0].guid
    else if (v.value.kind === 'bank') sel.value = firstWEM(v.value.bank.events) ?? ''
    ex.extra.audio = { kind: v.value.kind, wem: sel.value }
  } catch (e: any) { err.value = String(e) }
})
onBeforeUnmount(() => { delete ex.extra.audio })

function firstWEM(nodes: any[]): string | undefined {
  for (const n of nodes ?? []) {
    if (n.wem) return n.wem
    const w = firstWEM(n.children)
    if (w) return w
  }
}
function play(wem: string) {
  autoplay.value = true
  sel.value = wem
  if (ex.extra.audio) ex.extra.audio.wem = wem
}
async function exportAll(fmt: 'wav' | 'ogg') {
  try {
    const dir = await api.ExportSoundAll(p.guid, fmt)
    if (dir) flash(`Saved to ${dir}`)
  } catch (e: any) { flash(`Export failed: ${e}`) }
}

const hexType = (t: number) => '0x' + t.toString(16).toUpperCase().padStart(3, '0')
const secs = (s: number) => s.toFixed(2) + ' s'
const objects = computed(() => {
  const o = v.value?.bank?.objects ?? []
  const f = filter.value.trim().toLowerCase()
  return f ? o.filter((r: any) => (r.id + ' ' + r.typeName + ' ' + (r.detail ?? '') + ' ' + (r.parent ?? '')).toLowerCase().includes(f)) : o
})
const counts = computed(() => Object.entries(v.value?.bank?.counts ?? {}).sort((a: any, b: any) => b[1] - a[1]))
</script>

<template>
  <div class="flex h-full min-h-0">
    <div class="selectable flex min-w-0 flex-1 flex-col gap-4 overflow-auto p-5 text-[12.5px]">
      <div v-if="err" class="rounded border border-v-err/40 bg-v-err/10 p-3">
        <b class="text-v-err">This audio resource can't be shown.</b>
        <div class="mono mt-1 text-dim">{{ err }}</div>
      </div>
      <p v-else-if="!v" class="text-dim">loading…</p>

      <template v-else>
        <section v-if="sel">
          <div class="mb-1 flex items-center gap-2 text-dim">
            <span>WEM</span><Guid :guid="sel" />
          </div>
          <AudioPlayer :key="sel" :guid="sel" :autoplay="autoplay" />
        </section>

        <!-- WEM -->
        <template v-if="v.kind === 'wem'">
          <p v-if="v.wem.error" class="text-v-err">{{ v.wem.error }}</p>
          <section>
            <h4 class="mb-1 text-dim">Listed by {{ v.wem.sounds.length }} sound(s)</h4>
            <p v-if="!v.ready" class="text-faint">reference index still building…</p>
            <div class="flex flex-wrap gap-x-4"><Guid v-for="g in v.wem.sounds" :key="g" :guid="g" /></div>
            <template v-if="v.wem.banks.length">
              <h4 class="mt-3 mb-1 text-dim">Played by the Sound objects of</h4>
              <div class="flex flex-wrap gap-x-4"><Guid v-for="g in v.wem.banks" :key="g" :guid="g" /></div>
            </template>
            <template v-if="v.wem.users.length">
              <h4 class="mt-3 mb-1 text-dim">Other users</h4>
              <div class="flex flex-wrap gap-x-4"><Guid v-for="g in v.wem.users" :key="g" :guid="g" /></div>
              <p v-if="v.wem.voice" class="mt-1 text-[11.5px] text-faint">Voice WEMs are named by voice-line STUs (0x059) and most likely played as the External Source of the voice sound 0x0D400000000016F3 (docs/AssetFormats.md §5.5) [?].</p>
            </template>
          </section>
        </template>

        <!-- sound -->
        <template v-if="v.kind === 'sound'">
          <p v-if="v.sound.error" class="text-v-err">{{ v.sound.error }}</p>
          <section>
            <div class="mb-1 flex items-center gap-3">
              <h4 class="text-dim">WEMs ({{ v.sound.files.length }})</h4>
              <span class="flex-1" />
              <button v-if="v.sound.files.length" class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="exportAll('wav')">Export all WAV</button>
              <button v-if="v.sound.files.length" class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="exportAll('ogg')">Export all OGG</button>
            </div>
            <p v-if="!v.sound.files.length" class="text-faint">none</p>
            <table v-else class="w-full whitespace-nowrap">
              <thead class="text-left text-[11.5px] text-faint"><tr><th /><th class="pr-3">WEM</th><th class="pr-3">Wwise id</th><th class="pr-3">length</th><th class="pr-3">ch</th><th class="pr-3">rate</th><th /></tr></thead>
              <tbody>
                <tr v-for="f in v.sound.files" :key="f.guid" class="border-t border-line/50" :class="f.guid === sel ? 'bg-sel/60' : 'hover:bg-raised'">
                  <td class="py-0.5 pr-2"><button class="rounded px-1 text-v-num hover:bg-sel" title="Play" @click="play(f.guid)">▶</button></td>
                  <td class="pr-3"><Guid :guid="f.guid" /></td>
                  <td class="mono pr-3 text-dim">{{ f.id }}</td>
                  <td class="pr-3">{{ secs(f.seconds) }}</td>
                  <td class="pr-3">{{ f.channels }}</td>
                  <td class="pr-3">{{ f.rate }}</td>
                  <td class="text-faint">{{ f.stream ? 'stream' : '' }}<span v-if="f.error" class="text-v-err">{{ f.error }}</span></td>
                </tr>
              </tbody>
            </table>
          </section>
          <section>
            <h4 class="mb-1 text-dim">Events in the bank</h4>
            <p v-if="!v.sound.events.length" class="text-faint">none</p>
            <HircTree v-for="(e, i) in v.sound.events" :key="i" :node="e" :selected="sel" @play="play" />
          </section>
        </template>

        <!-- bank -->
        <template v-if="v.kind === 'bank'">
          <p v-if="v.bank.error" class="text-v-err">{{ v.bank.error }}</p>
          <section>
            <h4 class="mb-1 text-dim">Events ({{ v.bank.events.length }})</h4>
            <p v-if="!v.bank.events.length" class="text-faint">none</p>
            <HircTree v-for="(e, i) in v.bank.events" :key="i" :node="e" :selected="sel" @play="play" />
          </section>
          <section class="min-h-0">
            <div class="mb-1 flex items-center gap-3">
              <h4 class="text-dim">HIRC objects ({{ v.bank.objects.length }})</h4>
              <input v-model="filter" placeholder="filter: id, type, detail" class="w-56 rounded border border-line bg-well px-2 py-0.5">
            </div>
            <table class="w-full">
              <thead class="text-left text-[11.5px] text-faint"><tr><th class="pr-3">id</th><th class="pr-3">type</th><th class="pr-3">parent</th><th class="pr-3">detail</th><th>size</th></tr></thead>
              <tbody>
                <tr v-for="o in objects" :key="o.id + o.offset" class="border-t border-line/50 hover:bg-raised">
                  <td class="mono pr-3 text-dim">{{ o.id }}</td>
                  <td class="pr-3 text-v-obj">{{ o.typeName }}</td>
                  <td class="mono pr-3 text-faint">{{ o.parent }}</td>
                  <td class="pr-3">{{ o.detail }}
                    <template v-if="o.wem"><button class="rounded px-1 text-v-num hover:bg-sel" @click="play(o.wem)">▶</button><Guid :guid="o.wem" /></template></td>
                  <td class="text-faint">{{ o.size }}</td>
                </tr>
              </tbody>
            </table>
          </section>
        </template>

        <!-- switch group, switch, parameter, state group -->
        <template v-if="v.kind === 'named'">
          <section>
            <div class="text-[18px]">{{ v.named.name || '(name stripped)' }}</div>
            <div class="text-dim">{{ v.named.label }} · Wwise id <span class="mono">{{ v.named.id }}</span>
              <span v-if="v.named.hashOk" class="ml-2 text-v-ok" title="The id is FNV-1 (32-bit) of the lower-cased name">= FNV-1(name) ✓</span>
              <span v-else-if="v.named.name" class="ml-2 text-v-err">≠ FNV-1(name)</span></div>
            <p v-if="v.named.error" class="text-v-err">{{ v.named.error }}</p>
          </section>
          <section v-if="v.named.group">
            <h4 class="mb-1 text-dim">Group</h4>
            <Guid :guid="v.named.group.guid" /> <span class="ml-2">{{ v.named.group.name || '(stripped)' }}</span>
          </section>
          <section v-if="v.named.switches.length">
            <h4 class="mb-1 text-dim">Switches ({{ v.named.switches.length }})</h4>
            <div v-for="s in v.named.switches" :key="s.guid" class="flex gap-3"><Guid :guid="s.guid" /><span>{{ s.name || '(stripped)' }}</span><span class="mono text-faint">{{ s.id }}</span></div>
          </section>
          <section v-if="v.named.same.length">
            <h4 class="mb-1 text-dim">Same Wwise id</h4>
            <div v-for="s in v.named.same" :key="s.guid" class="flex gap-3"><Guid :guid="s.guid" /><span>{{ s.name }}</span><span class="text-faint">{{ hexType(s.type) }}</span></div>
          </section>
          <section>
            <h4 class="mb-1 text-dim">Banks holding the id ({{ v.named.banks.length }})</h4>
            <p class="mb-1 text-[11.5px] text-faint">The id occurs in the bank's HIRC objects (a byte match; the node parameters holding it aren't parsed) [?].</p>
            <p v-if="!v.ready" class="text-faint">reference index still building…</p>
            <div class="flex flex-wrap gap-x-4"><Guid v-for="g in v.named.banks" :key="g" :guid="g" /></div>
          </section>
        </template>
      </template>
    </div>

    <template v-if="v && (v.kind === 'wem' || v.kind === 'sound' || v.kind === 'bank')">
      <Splitter id="audio.side" :def="460" :min="280" :max="900" side="after" />
      <aside class="selectable min-h-0 shrink-0 overflow-auto border-l border-line p-4 text-[12.5px]" :style="{ width: sideW + 'px' }">
        <template v-if="v.kind === 'wem' && v.wem.header">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
            <dt class="text-dim">Codec</dt><dd>Wwise Vorbis (0x{{ v.wem.header.format.toString(16) }})</dd>
            <dt class="text-dim">Channels</dt><dd>{{ v.wem.header.channels }} <span class="mono text-faint">mask 0x{{ v.wem.header.channelMask.toString(16) }}</span></dd>
            <dt class="text-dim">Rate</dt><dd>{{ v.wem.header.sampleRate }} Hz</dd>
            <dt class="text-dim">Length</dt><dd>{{ secs(v.wem.seconds) }} · {{ v.wem.header.samples }} samples</dd>
            <dt class="text-dim">Bitrate</dt><dd>{{ Math.round(v.wem.header.avgBytesPerSecond * 8 / 1000) }} kbit/s</dd>
            <dt class="text-dim">Blocks</dt><dd>{{ v.wem.header.blocksize0 }} / {{ v.wem.header.blocksize1 }}</dd>
            <dt class="text-dim">Packets</dt><dd>{{ v.wem.packets }} <span class="text-faint">(setup at +{{ v.wem.header.setupOffset }}, audio at +{{ v.wem.header.audioOffset }})</span></dd>
            <dt class="text-dim">Mod signal</dt><dd class="mono">0x{{ v.wem.header.modSignal.toString(16) }}</dd>
            <dt class="text-dim">UID</dt><dd class="mono">{{ v.wem.header.uid.toString(16).padStart(8, '0') }}</dd>
            <dt v-if="v.wem.id" class="text-dim">Wwise id</dt><dd v-if="v.wem.id" class="mono">{{ v.wem.id }}</dd>
            <dt v-if="v.wem.header.hasLoop" class="text-dim">Loop</dt><dd v-if="v.wem.header.hasLoop">{{ v.wem.header.loopStart }} – {{ v.wem.header.loopEnd }}</dd>
          </dl>
          <h4 class="mt-4 mb-1 text-dim">RIFF chunks</h4>
          <table class="w-full"><tbody>
            <tr v-for="c in v.wem.header.chunks" :key="c.offset" class="border-t border-line/50"><td class="mono pr-3">{{ c.id }}</td><td class="text-dim">+0x{{ c.offset.toString(16) }}</td><td class="text-faint">{{ c.size }} bytes</td></tr>
          </tbody></table>
        </template>
        <template v-if="v.kind === 'sound'">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
            <dt class="text-dim">Bank</dt><dd><Guid v-if="v.sound.bank" :guid="v.sound.bank" /><span v-else class="text-faint">none</span></dd>
            <dt class="text-dim">Bank id</dt><dd class="mono">{{ v.sound.bankId }}</dd>
            <dt class="text-dim">Play event</dt><dd class="mono">{{ v.sound.eventPlay }}</dd>
            <dt class="text-dim">Stop event</dt><dd class="mono">{{ v.sound.eventStop }}</dd>
          </dl>
          <p class="mt-3 text-[11.5px] text-faint">Names of sounds, events and banks are stripped in 0.8; ids are Wwise's (FNV-1 of names we don't have). The Tree tab shows every field.</p>
        </template>
        <template v-if="v.kind === 'bank'">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
            <dt class="text-dim">Version</dt><dd>{{ v.bank.version }}</dd>
            <dt class="text-dim">Bank id</dt><dd class="mono">{{ v.bank.id }}</dd>
            <dt class="text-dim">Chunks</dt><dd><span v-for="c in v.bank.chunks" :key="c.offset" class="mr-3"><span class="mono">{{ c.id }}</span> <span class="text-faint">{{ c.size }}</span></span></dd>
            <dt class="text-dim">Sounds</dt><dd><Guid v-for="g in v.bank.sounds" :key="g" :guid="g" class="mr-3" /><span v-if="!v.bank.sounds.length" class="text-faint">none</span></dd>
          </dl>
          <div v-if="v.bank.sounds.length" class="mt-2 flex gap-2">
            <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="exportAll('wav')">Export all WAV</button>
            <button class="rounded px-2 py-0.5 text-dim hover:bg-raised hover:text-fg" @click="exportAll('ogg')">Export all OGG</button>
          </div>
          <h4 class="mt-4 mb-1 text-dim">Objects</h4>
          <div v-for="[k, n] in counts" :key="k" class="flex justify-between"><span>{{ k }}</span><span class="text-dim">{{ n }}</span></div>
          <h4 class="mt-4 mb-1 text-dim">Switch / state / parameter ids ({{ v.bank.groups.length }})</h4>
          <p class="mb-1 text-[11.5px] text-faint">found in the objects' bytes [?]</p>
          <div v-for="gr in v.bank.groups" :key="gr.guid" class="flex gap-3"><Guid :guid="gr.guid" /><span>{{ gr.name || '(stripped)' }}</span><span class="text-faint">{{ hexType(gr.type) }}</span></div>
        </template>
      </aside>
    </template>
  </div>
</template>
