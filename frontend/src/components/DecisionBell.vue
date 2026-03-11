<script setup lang="ts">
import { Bell } from 'lucide-vue-next'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

defineProps<{
  count: number
}>()

defineEmits<{
  click: []
}>()
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <button
        class="relative inline-flex items-center justify-center rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        :aria-label="`${count} pending decisions`"
        @click="$emit('click')"
      >
        <Bell class="size-4" />
        <span
          v-if="count > 0"
          class="absolute -top-0.5 -right-0.5 flex items-center justify-center"
        >
          <span class="absolute inline-flex h-3.5 w-3.5 rounded-full bg-amber-500/50 animate-ping" />
          <span class="relative inline-flex items-center justify-center h-3.5 min-w-[14px] rounded-full bg-amber-500 text-white text-[9px] font-bold px-0.5">
            {{ count }}
          </span>
        </span>
      </button>
    </TooltipTrigger>
    <TooltipContent>
      <span v-if="count > 0">{{ count }} pending decision{{ count !== 1 ? 's' : '' }} — click to view</span>
      <span v-else>No pending decisions</span>
    </TooltipContent>
  </Tooltip>
</template>
