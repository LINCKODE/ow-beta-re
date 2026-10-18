<script setup lang="ts">
// Entity definition: its components, their Statescript graphs and the resources they reference.
import { onMounted, ref } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { api } from '~/composables/api'

const p = defineProps<{ guid: string }>()
const { s, open } = useExplorer()
const e = ref<any>(null)
const err = ref('')
onMounted(async () => { try { e.value = await api.Entity(p.guid) } catch (x: any) { err.value = String(x) } })
</script>

<template>
  <div v-if="err" class="p-6 text-v-err">{{ err }}</div>
  <div v-else-if="e" class="selectable h-full overflow-auto px-5 py-4">
    <p class="mb-3 text-dim">{{ e.components.length }} components</p>
    <article v-for="c in e.components" :key="c.keyHash" class="mb-3 border-l-2 border-line-strong pl-4">
      <header class="flex items-baseline gap-3">
        <h3 class="text-[14px] font-medium">{{ c.type || c.key }}</h3>
        <button class="mono text-[12px] text-v-obj hover:underline" @click="s.tab = 'tree'; s.instance = c.instance">→ {{ c.instance }}</button>
      </header>
      <ul v-if="c.graphs.length" class="mt-1">
        <li v-for="g in c.graphs" :key="g.guid" class="flex items-baseline gap-3 py-0.5">
          <span class="text-dim">graph</span>
          <button class="hover:underline" @click="open(g.guid, { tab: 'graph' })"><Guid :guid="g.guid" :link="false" /></button>
          <span v-if="g.overrides" class="text-faint">{{ g.overrides }} overrides</span>
        </li>
      </ul>
      <ul v-if="c.refs.length" class="mt-1 text-[12px]">
        <li v-for="(r, k) in c.refs.filter((x: any) => !c.graphs.some((g: any) => g.guid === x.guid))" :key="k" class="flex items-baseline gap-3 py-0.5">
          <span class="w-64 shrink-0 truncate text-dim" :title="r.path">{{ r.path }}</span>
          <Guid :guid="r.guid" />
          <span v-if="r.name" class="truncate text-dim">{{ r.name }}</span>
        </li>
      </ul>
    </article>
    <template v-if="e.refs.length">
      <h3 class="mt-4 mb-1 font-medium">On the definition</h3>
      <ul class="text-[12px]">
        <li v-for="(r, k) in e.refs" :key="k" class="flex gap-3 py-0.5"><span class="w-64 truncate text-dim">{{ r.path }}</span><Guid :guid="r.guid" /><span class="text-dim">{{ r.name }}</span></li>
      </ul>
    </template>
  </div>
</template>
