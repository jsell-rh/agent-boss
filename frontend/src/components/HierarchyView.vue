<script setup lang="ts">
import type { HierarchyTree, HierarchyNode, AgentUpdate } from '@/types'
import { computed } from 'vue'
import { Network, ArrowUp, ArrowDown } from 'lucide-vue-next'
import StatusBadge from './StatusBadge.vue'
import AgentAvatar from './AgentAvatar.vue'
import { Badge } from '@/components/ui/badge'

const props = defineProps<{
  tree: HierarchyTree
  agents: Record<string, AgentUpdate>
}>()

const emit = defineEmits<{
  'select-agent': [name: string]
}>()

const hasHierarchy = computed(() =>
  Object.values(props.agents).some(a => a.parent)
)

function renderNodeList(names: string[], depth: number): { name: string; node: HierarchyNode; depth: number }[] {
  const result: { name: string; node: HierarchyNode; depth: number }[] = []
  for (const name of names) {
    const node = props.tree.nodes[name]
    if (!node) continue
    result.push({ name, node, depth })
    if (node.children?.length) {
      result.push(...renderNodeList(node.children, depth + 1))
    }
  }
  return result
}

const flatTree = computed(() => {
  if (!props.tree.roots?.length) return []
  return renderNodeList(props.tree.roots, 0)
})
</script>

<template>
  <div class="space-y-4">
    <!-- Empty state when no hierarchy declared -->
    <div v-if="!hasHierarchy" class="flex flex-col items-center justify-center py-16 text-center">
      <Network class="size-10 text-muted-foreground/40 mb-3" aria-hidden="true" />
      <p class="text-sm font-medium text-muted-foreground">No hierarchy declared</p>
      <p class="text-xs text-muted-foreground/60 mt-1">
        Agents can declare a parent via status POST or /register to appear here.
      </p>
    </div>

    <!-- Flat-tree rendering -->
    <div v-else class="space-y-1">
      <div
        v-for="{ name, node, depth } in flatTree"
        :key="name"
        class="flex items-center gap-2 rounded-md px-3 py-2 cursor-pointer hover:bg-accent/50 transition-colors group"
        :style="{ paddingLeft: `${12 + depth * 24}px` }"
        @click="emit('select-agent', name)"
      >
        <!-- Tree connector -->
        <span
          v-if="depth > 0"
          class="text-muted-foreground/40 text-xs shrink-0 select-none font-mono"
          aria-hidden="true"
        >
          └
        </span>

        <!-- Avatar -->
        <AgentAvatar :name="name" :size="24" class="shrink-0" aria-hidden="true" />

        <!-- Name -->
        <span class="text-sm font-semibold truncate group-hover:text-primary transition-colors">
          {{ name }}
        </span>

        <!-- Role badge -->
        <Badge
          v-if="node.role"
          variant="outline"
          class="text-[10px] h-5 px-1.5 border-purple-500/40 text-purple-600 dark:text-purple-400 shrink-0"
        >
          {{ node.role }}
        </Badge>

        <!-- Status -->
        <StatusBadge
          v-if="agents[name]"
          :status="agents[name].status"
          class="shrink-0"
        />

        <!-- Depth badge -->
        <Badge
          variant="secondary"
          class="text-[10px] h-5 px-1.5 shrink-0 ml-auto"
          :title="`Depth ${node.depth}`"
        >
          L{{ node.depth }}
        </Badge>

        <!-- Children count -->
        <span
          v-if="node.children?.length"
          class="inline-flex items-center gap-0.5 text-[10px] text-muted-foreground shrink-0"
          :title="`Manages ${node.children.length} agent${node.children.length === 1 ? '' : 's'}`"
        >
          <ArrowDown class="size-3" />
          {{ node.children.length }}
        </span>

        <!-- Summary snippet -->
        <span
          v-if="agents[name]?.summary"
          class="text-xs text-muted-foreground truncate hidden md:block"
        >
          {{ agents[name].summary.replace(/^[^:]+:\s*/, '').slice(0, 60) }}
        </span>
      </div>
    </div>

    <!-- Legend -->
    <div v-if="hasHierarchy" class="flex items-center gap-4 text-[11px] text-muted-foreground pt-2 border-t">
      <span class="inline-flex items-center gap-1"><ArrowUp class="size-3" /> Reports to parent</span>
      <span class="inline-flex items-center gap-1"><ArrowDown class="size-3" /> Manages children</span>
      <span>L0 = root, L1 = first level, …</span>
    </div>
  </div>
</template>
