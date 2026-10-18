<script setup lang="ts">
// A bank object and its subtree (App.Audio: events → actions → targets → children by parent id → sounds → WEMs).
import { ref } from 'vue'

const p = defineProps<{ node: any; depth?: number; selected?: string }>()
const emit = defineEmits<{ play: [wem: string] }>()
const open = ref((p.depth ?? 0) < 3)
const secs = (s: number) => (s ? s.toFixed(2) + ' s' : '')
</script>

<template>
  <div>
    <div class="flex items-center gap-1.5 rounded py-0.5 hover:bg-raised" :style="{ paddingLeft: (depth ?? 0) * 14 + 'px' }"
         :class="node.wem && node.wem === selected ? 'bg-sel/60' : ''">
      <button v-if="node.children?.length" class="w-4 text-faint" @click="open = !open">{{ open ? '▾' : '▸' }}</button>
      <span v-else class="w-4" />
      <span :class="node.missing ? 'text-faint' : 'text-v-obj'">{{ node.typeName }}</span>
      <span class="mono text-[11.5px] text-faint">{{ node.id }}</span>
      <span class="truncate text-dim">{{ node.detail }}</span>
      <template v-if="node.wem">
        <button class="rounded px-1 text-v-num hover:bg-sel" title="Play this WEM" @click="emit('play', node.wem)">▶</button>
        <Guid :guid="node.wem" />
        <span class="text-faint">{{ secs(node.seconds) }}</span>
      </template>
    </div>
    <template v-if="open">
      <HircTree v-for="(c, i) in node.children" :key="i" :node="c" :depth="(depth ?? 0) + 1" :selected="selected" @play="w => emit('play', w)" />
      <div v-if="node.more" class="text-faint" :style="{ paddingLeft: ((depth ?? 0) + 1) * 14 + 20 + 'px' }">… {{ node.more }} more</div>
    </template>
  </div>
</template>
