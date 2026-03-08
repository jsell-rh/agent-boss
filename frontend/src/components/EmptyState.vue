<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{
  icon: Component
  title: string
  description?: string
}>()
</script>

<template>
  <div class="flex flex-col items-center justify-center py-16 text-center gap-3">
    <div class="rounded-full bg-muted p-3.5">
      <component :is="icon" class="size-6 text-muted-foreground/60" aria-hidden="true" />
    </div>
    <div class="space-y-1">
      <p class="text-sm font-medium text-foreground">{{ title }}</p>
      <p v-if="description" class="text-xs text-muted-foreground">{{ description }}</p>
    </div>
    <!-- Optional slot for action buttons -->
    <slot />
  </div>
</template>
