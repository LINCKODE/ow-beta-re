<script setup lang="ts">
import { typeColor } from '~/utils/format'
defineProps<{ type: number }>()
</script>
<template>
  <span class="inline-block h-2.5 w-2.5 shrink-0 rounded-[3px]" :style="{ background: typeColor(type, 0.72, 0.13) }" />
</template>
