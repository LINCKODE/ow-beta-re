<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { mainPane, useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'
import { parseGuid } from '~/utils/format'

const { s, open, refresh } = useExplorer()
// back / forward act on the focused pane (split view)
const f = computed(() => useExplorer(s.focus ?? mainPane))
const back = () => f.value.back()
const forward = () => f.value.forward()
const text = ref('')
const input = ref<HTMLInputElement>()
let t: any
watch(text, (v) => { clearTimeout(t); t = setTimeout(() => { s.filter.search = v.trim() }, 160) })

function enter() {
  const v = text.value.trim()
  const g = parseGuid(v)
  // A full GUID (or ow:// link) opens directly; anything else filters the list.
  if (g && (v.replace(/^(ow:\/\/|0x)/i, '').length >= 13)) open(g)
}
function clear() { text.value = ''; s.filter.search = ''; input.value?.blur() }
async function choose() { s.status = await api.ChooseInstall(); await refresh() }
defineExpose({ focus: () => { input.value?.focus(); input.value?.select() } })
</script>

<template>
  <header class="flex h-12 items-center gap-3 border-b border-line bg-panel px-3">
    <div class="flex items-center gap-1">
      <button class="rounded px-2 py-1 text-dim hover:bg-raised hover:text-fg disabled:opacity-30" :disabled="f.s.cursor <= 0" title="Back (Alt+←)" @click="back">‹</button>
      <button class="rounded px-2 py-1 text-dim hover:bg-raised hover:text-fg disabled:opacity-30" :disabled="f.s.cursor >= f.s.history.length - 1" title="Forward (Alt+→)" @click="forward">›</button>
    </div>
    <div class="relative max-w-[560px] flex-1">
      <input
        ref="input" v-model="text"
        class="mono w-full rounded-md border border-line bg-well py-1.5 pr-16 pl-3 text-fg placeholder:font-sans placeholder:text-faint"
        placeholder="Search GUIDs or names, or paste ow://…"
        spellcheck="false" @keydown.enter="enter" @keydown.esc="clear"
      />
      <kbd class="mono pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded border border-line px-1.5 text-[11px] text-faint">/</kbd>
    </div>
    <div class="ml-auto flex min-w-0 items-center gap-3">
      <span class="truncate text-dim" :title="s.status?.install">{{ s.status?.install }}</span>
      <button class="shrink-0 rounded border border-line px-2.5 py-1 hover:bg-raised" @click="choose">Open install…</button>
    </div>
  </header>
</template>
