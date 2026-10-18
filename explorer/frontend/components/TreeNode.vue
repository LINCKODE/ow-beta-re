<script setup lang="ts">
// One value of a decoded tree. Children render lazily when expanded; long lists show 200 at a time.
import { computed, ref } from 'vue'
import { useExplorer } from '~/composables/useExplorer'

const p = defineProps<{ node: any; depth: number; openDefault?: boolean }>()
const emit = defineEmits<{ (e: 'jump', i: number): void }>()
const { s } = useExplorer()
const n = computed(() => p.node)
const hasKids = computed(() => (n.value.children?.length ?? 0) > 0)
const open = ref(p.openDefault || (p.depth < 2 && (n.value.children?.length ?? 0) <= 24))
const shown = ref(200)
const kids = computed(() => (n.value.children ?? []).slice(0, shown.value))
const kindClass: Record<string, string> = {
  prim: 'text-v-num', string: 'text-v-string', enum: 'text-v-enum', object: 'text-v-obj', null: 'text-faint', error: 'text-v-err',
}
const deep = /ow:\/\/([0-9a-fA-F]{1,16})/g
const strParts = computed(() => {
  if (n.value.kind !== 'string') return []
  const out: { t: string; g?: string }[] = []
  let last = 0
  for (const m of (n.value.value ?? '').matchAll(deep)) {
    out.push({ t: n.value.value.slice(last, m.index) }, { t: m[0], g: m[1] })
    last = m.index! + m[0].length
  }
  out.push({ t: (n.value.value ?? '').slice(last) })
  return out
})
</script>

<template>
  <div class="text-[12.5px]">
    <div
      class="group flex items-baseline gap-2 rounded px-1 py-[1px] hover:bg-raised"
      :class="s.path === n.path ? 'bg-sel' : ''"
      :style="{ paddingLeft: depth * 14 + 4 + 'px' }"
      @click="s.path = n.path; hasKids && (open = !open)"
    >
      <span class="w-3 shrink-0 text-faint">{{ hasKids ? (open ? '▾' : '▸') : '' }}</span>
      <span :class="n.named === false && n.label?.startsWith('0x') ? 'mono text-dim' : 'text-fg'">{{ n.label }}</span>
      <template v-if="n.kind === 'ref'">
        <Guid v-if="n.value !== 'none'" :guid="n.guid" :missing="n.missing" />
        <span v-else class="text-faint">none</span>
        <span v-if="n.refName" class="truncate text-dim">{{ n.refName }}</span>
      </template>
      <button v-else-if="n.kind === 'object'" class="mono text-v-obj hover:underline" @click.stop="emit('jump', n.instance)">→ {{ n.instance }}</button>
      <span v-else-if="n.kind === 'string'" class="text-v-string">“<template v-for="(x, k) in strParts" :key="k"><Guid v-if="x.g" :guid="x.g" /><template v-else>{{ x.t }}</template></template>”</span>
      <span v-else-if="n.value && n.kind !== 'inline'" class="mono" :class="kindClass[n.kind]">{{ n.value }}</span>
      <span v-if="n.alt" class="mono text-faint">{{ n.alt }}</span>
      <span v-if="n.kind === 'list' || n.kind === 'map'" class="mono text-faint">{{ n.count }}</span>
      <span class="ml-auto shrink-0 truncate pl-3 text-[11px] text-faint" :class="n.kind === 'object' ? 'text-dim' : ''">{{ n.type }}</span>
    </div>
    <template v-if="open && hasKids">
      <TreeNode v-for="c in kids" :key="c.path" :node="c" :depth="depth + 1" @jump="(i: number) => emit('jump', i)" />
      <button v-if="n.children.length > shown" class="ml-10 text-[12px] text-focus hover:underline" @click="shown += 500">
        Show more ({{ n.children.length - shown }} left)
      </button>
    </template>
  </div>
</template>
