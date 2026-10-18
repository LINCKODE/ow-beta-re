<script setup lang="ts">
// Open a selection's resource elsewhere (tab / split pane / new window) and ask Claude about the selection.
import { useExplorer, type Tab } from '~/composables/useExplorer'
import { askAbout, type AskSubject } from '~/composables/useAsk'

const p = defineProps<{ guid?: string; tab?: Tab; instance?: number; ask?: AskSubject; label?: string }>()
const { open } = useExplorer()
const o = () => ({ tab: p.tab, instance: p.instance })
</script>

<template>
  <span class="inline-flex items-center gap-0.5 text-[11.5px]">
    <template v-if="guid">
      <span v-if="label" class="mr-1 text-faint">{{ label }}</span>
      <button class="btn" title="Open in a new tab" @click.stop="open(guid, { ...o(), newTab: true })">⧉ tab</button>
      <button class="btn" title="Open in the split pane on the right" @click.stop="open(guid, { ...o(), split: true })">◫ split</button>
      <button class="btn" title="Open in a new window" @click.stop="open(guid, { ...o(), newWindow: true })">⊞ window</button>
    </template>
    <button v-if="ask" class="btn text-focus" title="Ask the Claude session that watches the explorer about this" @click.stop="askAbout(ask)">✦ Ask Claude</button>
  </span>
</template>

<style scoped>
.btn { border-radius: 4px; padding: 1px 6px; color: var(--color-dim); white-space: nowrap; }
.btn:hover { background: var(--color-raised); color: var(--color-fg); }
.btn.text-focus { color: var(--color-focus); }
</style>
