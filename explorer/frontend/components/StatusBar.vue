<script setup lang="ts">
import { computed } from 'vue'
import { useExplorer } from '~/composables/useExplorer'
import { num } from '~/utils/format'
import { api } from '~/composables/api'
import { ask, askToggle } from '~/composables/useAsk'

const { s, flash } = useExplorer()
// MCP server chip: port, calls, the drive toggle (off at every start; Claude's open / select tools need it on)
async function toggleDrive() {
  if (!s.mcp?.port) return
  s.mcp = await api.SetMcpDrive(!s.mcp.drive)
  flash(s.mcp.drive ? 'Claude may now open resources and change the selection (MCP drive on)' : 'MCP drive off: Claude can only look')
}
const mcpTitle = computed(() => {
  const m = s.mcp
  if (!m) return ''
  if (!m.port) return `MCP server off: ${m.error}`
  return `MCP server on 127.0.0.1:${m.ports.join(', ')}${m.primary ? ' (the .mcp.json port)' : ''} · ${m.requests} calls` +
    (m.failed ? `, ${m.failed} failed` : '') + (m.last ? ` · last: ${m.last}` : '') +
    `\nClick: ${m.drive ? 'stop' : 'allow'} Claude opening resources and changing the selection (drive tools)`
})
const ix = computed(() => s.status?.index)
const pct = computed(() => (ix.value?.total ? Math.round((100 * ix.value.done) / ix.value.total) : 0))
</script>

<template>
  <footer class="flex h-7 items-center gap-4 overflow-hidden border-t border-line bg-panel px-3 text-[12px] whitespace-nowrap text-dim">
    <template v-if="s.status?.open">
      <span class="min-w-0 truncate" :title="s.status.describe">{{ s.status.describe }}</span>
      <span class="shrink-0">opened in {{ s.status.openMs }} ms</span>
      <span v-if="ix?.phase && ix.phase !== 'done'" class="flex items-center gap-2">
        Indexing {{ ix.phase }}
        <span class="relative h-1 w-24 overflow-hidden rounded bg-line"><span class="absolute inset-y-0 left-0 bg-focus" :style="{ width: pct + '%' }" /></span>
        {{ pct }}%
      </span>
      <span v-else-if="ix?.phase === 'done'" class="min-w-0 truncate" :title="`${ix.failed} unreadable`">{{ num(ix.refs) }} reference pairs, {{ num(ix.named) }} names {{ ix.cached ? 'from the index cache' : 'indexed' }} in {{ ix.seconds.toFixed(1) }} s</span>
      <span v-else-if="ix?.phase === 'failed'" class="text-v-err">reference index failed</span>
    </template>
    <span v-else-if="s.status?.error" class="text-v-err">{{ s.status.error }}</span>
    <span v-else>Opening the install…</span>
    <span v-if="s.mapCursor" class="mono shrink-0 text-fg">{{ s.mapCursor }}</span>
    <button v-if="s.mcp?.port" class="ml-auto flex shrink-0 items-center gap-1 rounded px-1.5 hover:bg-raised" :class="ask.open ? 'text-fg' : 'text-focus'" title="Ask Claude: questions about the selection go to the Claude Code session that watches the explorer" @click="askToggle">✦ Ask</button>
    <button v-if="s.mcp" :class="s.mcp.port ? '' : 'ml-auto'" class="flex shrink-0 items-center gap-1.5 rounded px-1.5 hover:bg-raised" :title="mcpTitle" @click="toggleDrive">
      <span class="h-1.5 w-1.5 rounded-full" :class="!s.mcp.port ? 'bg-line' : s.mcp.drive ? 'bg-v-err' : 'bg-focus'" />
      <span :class="s.mcp.port ? 'text-dim' : 'text-faint'">MCP{{ s.mcp.port ? ' :' + s.mcp.port : ' off' }}</span>
      <span v-if="s.mcp.requests" class="text-faint">{{ s.mcp.requests }}</span>
      <span v-if="s.mcp.port" :class="s.mcp.drive ? 'text-v-err' : 'text-faint'">drive {{ s.mcp.drive ? 'on' : 'off' }}</span>
    </button>
    <span class="shrink-0 text-faint" :class="s.mcp ? '' : 'ml-auto'">/ search · Ctrl-click new tab · Alt+← / mouse back · Ctrl+W close tab · P pin</span>
  </footer>
</template>
