import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import type { KnowledgeSpace } from '@/types'

// --------------- Minimal stubs ---------------

vi.mock('@/api/client', () => ({
  default: {
    fetchPersonas: vi.fn().mockResolvedValue([]),
    fetchDefaultContracts: vi.fn().mockResolvedValue(''),
    fetchContracts: vi.fn().mockResolvedValue(''),
    exportFleet: vi.fn(),
  },
}))

// Stub all heavy child components — we only test chip logic
const stubAll = {
  ScrollArea: { template: '<div><slot /></div>' },
  Card: { template: '<div><slot /></div>' },
  CardContent: { template: '<div><slot /></div>' },
  Badge: { template: '<span><slot /></span>' },
  Button: { template: '<button @click="$emit(\'click\')"><slot /></button>', emits: ['click'] },
  Textarea: { template: '<textarea />' },
  Input: { template: '<input />' },
  Tooltip: { template: '<div><slot /></div>' },
  TooltipContent: { template: '<div><slot /></div>' },
  TooltipTrigger: { template: '<div><slot /></div>' },
  Tabs: { template: '<div><slot /></div>' },
  TabsContent: { template: '<div><slot /></div>' },
  TabsList: { template: '<div><slot /></div>' },
  TabsTrigger: { template: '<button><slot /></button>' },
  AlertDialog: { template: '<div><slot /></div>' },
  AlertDialogAction: { template: '<button><slot /></button>' },
  AlertDialogCancel: { template: '<button><slot /></button>' },
  AlertDialogContent: { template: '<div><slot /></div>' },
  AlertDialogDescription: { template: '<div><slot /></div>' },
  AlertDialogFooter: { template: '<div><slot /></div>' },
  AlertDialogHeader: { template: '<div><slot /></div>' },
  AlertDialogTitle: { template: '<div><slot /></div>' },
  Dialog: { template: '<div><slot /></div>' },
  DialogContent: { template: '<div><slot /></div>' },
  DialogDescription: { template: '<div><slot /></div>' },
  DialogHeader: { template: '<div><slot /></div>' },
  DialogTitle: { template: '<div><slot /></div>' },
  StatusBadge: { template: '<span />' },
  AgentAvatar: { template: '<span />' },
  AgentProfileCard: { template: '<div />' },
  GanttTimeline: { template: '<div />' },
  HierarchyView: { template: '<div />' },
  AgentCreateDialog: { template: '<div />' },
  ImportFleetModal: { template: '<div />', emits: ['update:open', 'imported'] },
  // icons
  Radio: { template: '<svg />' },
  Bell: { template: '<svg />' },
  Trash2: { template: '<svg />' },
  Archive: { template: '<svg />' },
  MessageSquare: { template: '<svg />' },
  SendHorizontal: { template: '<svg />' },
  HelpCircle: { template: '<svg />' },
  AlertTriangle: { template: '<svg />' },
  MessageSquareReply: { template: '<svg />' },
  GitBranch: { template: '<svg />' },
  ExternalLink: { template: '<svg />' },
  Clock: { template: '<svg />' },
  Layers: { template: '<svg />' },
  Search: { template: '<svg />' },
  Plus: { template: '<svg />' },
  RotateCcw: { template: '<svg />' },
  FileText: { template: '<svg />' },
  Pencil: { template: '<svg />' },
  X: { template: '<svg />' },
  Save: { template: '<svg />' },
  Loader2: { template: '<svg />' },
  Download: { template: '<svg />' },
  Upload: { template: '<svg />' },
}

import SpaceOverview from '@/components/SpaceOverview.vue'

const mockSpace: KnowledgeSpace = {
  name: 'My Space',
  agents: {},
  created_at: '',
  updated_at: '',
}

function makeWrapper(spaceOverride = {}) {
  return mount(SpaceOverview, {
    props: { space: { ...mockSpace, ...spaceOverride }, tmuxStatus: null },
    global: { stubs: stubAll },
  })
}

const LS_KEY = 'fleet-import-My Space'

describe('SpaceOverview fleet import chip', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('chip is hidden when no localStorage key exists', async () => {
    const wrapper = makeWrapper()
    await flushPromises()
    const vm = wrapper.vm as unknown as { fleetImportRelative: string | null }
    expect(vm.fleetImportRelative).toBeNull()
  })

  it('chip shows relative time when localStorage key is present', async () => {
    const ts = new Date(Date.now() - 5 * 60 * 1000).toISOString() // 5 min ago
    localStorage.setItem(LS_KEY, ts)

    const wrapper = makeWrapper()
    await flushPromises()
    const vm = wrapper.vm as unknown as { fleetImportRelative: string | null }
    expect(vm.fleetImportRelative).toMatch(/\d+m ago/)
  })

  it('chip updates after onFleetImported is called', async () => {
    const wrapper = makeWrapper()
    await flushPromises()
    const vm = wrapper.vm as unknown as {
      fleetImportRelative: string | null
      onFleetImported: () => void
    }
    expect(vm.fleetImportRelative).toBeNull()

    // Simulate a successful import writing to localStorage
    const ts = new Date().toISOString()
    localStorage.setItem(LS_KEY, ts)
    vm.onFleetImported()
    await flushPromises()

    expect(vm.fleetImportRelative).not.toBeNull()
    expect(vm.fleetImportRelative).toMatch(/ago|just now/)
  })
})
