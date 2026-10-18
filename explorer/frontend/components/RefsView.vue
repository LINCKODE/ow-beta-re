<script setup lang="ts">
// References: what the resource uses (left) and what uses it (right), from the reference index.
const p = defineProps<{ guid: string }>()
import { usePanel } from '~/composables/usePanel'
const leftW = usePanel('refs.left', 520)
</script>

<template>
  <div class="flex h-full min-h-0">
    <section class="flex min-h-0 shrink-0 flex-col border-r border-line" :style="{ width: leftW + 'px' }">
      <h3 class="border-b border-line px-3 py-1.5 font-medium">Uses <span class="font-normal text-faint">(references out)</span></h3>
      <NeighborList class="min-h-0 flex-1" :guid="p.guid" dir="out" />
    </section>
    <Splitter id="refs.left" :def="520" :min="260" :max="1400" />
    <section class="flex min-h-0 min-w-0 flex-1 flex-col">
      <h3 class="border-b border-line px-3 py-1.5 font-medium">Used by <span class="font-normal text-faint">(references in)</span></h3>
      <NeighborList class="min-h-0 flex-1" :guid="p.guid" dir="in" />
    </section>
  </div>
</template>
