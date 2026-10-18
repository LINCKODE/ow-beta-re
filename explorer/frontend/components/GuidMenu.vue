<script setup lang="ts">
// Context menu of a GUID (right-click on any <Guid>): open, open in a new tab, dependencies, copy, reveal.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { mainPane, useExplorer, type OpenOpts } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { guidType, hex3 } from '~/utils/format'
import { askAbout } from '~/composables/useAsk'

const { s, copy, reveal } = useExplorer()
// open in the pane the menu was opened from
const open = (g: string, o: OpenOpts = {}) => useExplorer(s.menu?.pane ?? mainPane).open(g, o)
const pkgs = ref<string[]>([])
const name = ref('')
const m = computed(() => s.menu)
watch(m, async (x) => {
  pkgs.value = []
  name.value = ''
  if (!x) return
  try {
    const r = await api.Resource(x.guid)
    pkgs.value = r.packages
    name.value = r.name?.text ?? ''
  } catch { /* not a resource of the install */ }
})
const pos = computed(() => {
  if (!m.value) return {}
  const x = Math.min(m.value.x, window.innerWidth - 260), y = Math.min(m.value.y, window.innerHeight - 390)
  return { left: x + 'px', top: y + 'px' }
})
function run(f: () => void) { f(); s.menu = null }
const close = (e: Event) => { if (s.menu && !(e.target as HTMLElement)?.closest?.('[data-guid-menu]')) s.menu = null }
const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') s.menu = null }
onMounted(() => { window.addEventListener('mousedown', close, true); window.addEventListener('keydown', esc) })
onBeforeUnmount(() => { window.removeEventListener('mousedown', close, true); window.removeEventListener('keydown', esc) })
</script>

<template>
  <div v-if="m" data-guid-menu class="fixed z-50 w-[250px] overflow-hidden rounded-md border border-line-strong bg-raised py-1 text-[12.5px] shadow-2xl shadow-black/50" :style="pos">
    <div class="border-b border-line px-3 pt-1 pb-1.5">
      <div class="mono text-dim">{{ m.guid }} · {{ hex3(guidType(m.guid)) }}</div>
      <div v-if="name" class="truncate">{{ name }}</div>
    </div>
    <button class="menu-item" @click="run(() => open(m!.guid))">Open</button>
    <button class="menu-item" @click="run(() => open(m!.guid, { newTab: true }))">Open in new tab <kbd>Ctrl+click</kbd></button>
    <button class="menu-item" @click="run(() => open(m!.guid, { newWindow: true }))">Open in new window <kbd>Shift+click</kbd></button>
    <button class="menu-item" @click="run(() => open(m!.guid, { split: true }))">Open in split view</button>
    <button class="menu-item" @click="run(() => askAbout({ kind: 'resource', guid: m!.guid, label: name || m!.guid }))">Ask Claude about it</button>
    <button class="menu-item" @click="run(() => open(m!.guid, { tab: 'deps' }))">Dependency graph</button>
    <button class="menu-item" @click="run(() => open(m!.guid, { tab: 'refs' }))">References in / out</button>
    <div class="my-1 border-t border-line" />
    <button class="menu-item" @click="run(() => copy(m!.guid, 'GUID copied'))">Copy GUID</button>
    <button class="menu-item" @click="run(() => copy('0x' + m!.guid.toUpperCase(), 'Copied'))">Copy as 0x…</button>
    <button class="menu-item" @click="run(() => copy('ow://' + m!.guid, 'Link copied'))">Copy ow:// link</button>
    <div class="my-1 border-t border-line" />
    <button class="menu-item" @click="run(() => reveal(m!.guid, 'type'))">Reveal in type list</button>
    <button class="menu-item" :disabled="!pkgs.length" @click="run(() => reveal(m!.guid, 'package', pkgs[0]))">Reveal in its package</button>
  </div>
</template>

<style scoped>
.menu-item { display: flex; width: 100%; align-items: baseline; justify-content: space-between; gap: 8px; padding: 4px 12px; text-align: left; }
.menu-item:hover:not(:disabled) { background: var(--color-sel); }
.menu-item:disabled { color: var(--color-faint); }
.menu-item kbd { font-family: var(--font-mono); font-size: 10.5px; color: var(--color-faint); }
</style>
