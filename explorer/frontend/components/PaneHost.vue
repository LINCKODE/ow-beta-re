<script setup lang="ts">
// Hosts one pane (main or split): everything inside uses this pane's resource, tabs and history.
import { provide } from 'vue'
import { PANE, useExplorer, type Pane } from '~/composables/useExplorer'

const p = defineProps<{ pane: Pane }>()
provide(PANE, p.pane)
const { s } = useExplorer(p.pane)
</script>

<template>
  <div
    class="flex min-h-0 min-w-0 flex-col" :data-pane-id="pane.id"
    :class="[s.split && s.focus === pane ? 'outline outline-1 -outline-offset-1 outline-focus/40' : '', s.docDrag?.over?.pane === pane.id && s.docDrag.from !== pane ? 'bg-focus/5 outline outline-2 -outline-offset-2 outline-focus/60' : '']"
    @mousedown.capture="s.focus = pane"
  >
    <ResourceView class="min-h-0 flex-1" />
  </div>
</template>
