import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import * as yaml from 'js-yaml'
import type { KnowledgeSpace } from '@/types'

// jsdom's FileReader dispatches events outside microtasks, so flushPromises() doesn't
// trigger onload. Stub it to use file.text() (Promise-based) so vitest can flush it.
vi.stubGlobal('FileReader', class {
  result: string | null = null
  onload: ((ev: { target: { result: string | null } }) => void) | null = null
  readAsText(file: File) {
    file.text().then(text => {
      this.result = text
      this.onload?.({ target: { result: text } })
    })
  }
})

// --------------- Minimal mocks ---------------

// Mock the api module
vi.mock('@/api/client', () => ({
  default: {
    exportFleet: vi.fn(),
    createPersona: vi.fn(),
    updatePersona: vi.fn(),
    createAgent: vi.fn(),
    updateAgentConfig: vi.fn(),
  },
}))

import ImportFleetModal from '@/components/ImportFleetModal.vue'
import api from '@/api/client'

// Stub child UI components to isolate ImportFleetModal logic
const stubComponents = {
  Dialog: { template: '<div><slot /></div>' },
  DialogContent: { template: '<div><slot /></div>' },
  DialogHeader: { template: '<div><slot /></div>' },
  DialogTitle: { template: '<div><slot /></div>' },
  DialogDescription: { template: '<div><slot /></div>' },
  Button: { template: '<button @click="$emit(\'click\')"><slot /></button>', emits: ['click'] },
  Badge: { template: '<span><slot /></span>' },
  ScrollArea: { template: '<div><slot /></div>' },
  Upload: { template: '<svg />' },
  CheckCircle2: { template: '<svg />' },
  AlertCircle: { template: '<svg />' },
  Loader2: { template: '<svg />' },
  Plus: { template: '<svg />' },
  RefreshCw: { template: '<svg />' },
  Minus: { template: '<svg />' },
}

const mockSpace: KnowledgeSpace = {
  name: 'Test Space',
  agents: {},
  created_at: '',
  updated_at: '',
}

function makeWrapper(spaceOverride = {}) {
  return mount(ImportFleetModal, {
    props: { open: true, space: { ...mockSpace, ...spaceOverride } },
    global: { stubs: stubComponents },
  })
}

// --------------- Fleet YAML fixtures ---------------

const validFleet = {
  version: '1',
  space: { name: 'Test Space' },
  personas: {
    arch: { name: 'Architecture Expert', prompt: 'You are an architect.' },
  },
  agents: {
    cto: { role: 'manager' },
    arch: { role: 'worker', parent: 'cto', personas: ['arch'], initial_prompt: 'You are an architect.' },
  },
}

const validFleetYaml = yaml.dump(validFleet)

function makeFileEvent(content: string, filename = 'fleet.yaml', size?: number): Event {
  const file = new File([content], filename, { type: 'application/yaml' })
  if (size !== undefined) {
    Object.defineProperty(file, 'size', { value: size })
  }
  const input = document.createElement('input')
  Object.defineProperty(input, 'files', { value: [file] })
  return { target: input } as unknown as Event
}

// --------------- Tests ---------------

describe('ImportFleetModal', () => {
  beforeEach(() => {
    vi.mocked(api.createPersona).mockResolvedValue({ id: 'arch', name: 'Architecture Expert', prompt: 'You are an architect.', description: '', version: 1, created_at: '', updated_at: '' })
    vi.mocked(api.createAgent).mockResolvedValue({ ok: true, agent: 'cto', backend: 'tmux', session: '', space: 'Test Space' })
    vi.mocked(api.updateAgentConfig).mockResolvedValue({ work_dir: '' })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  // --- File validation ---

  it('shows error for non-yaml file', async () => {
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as { onFileInput: (e: Event) => void; parseError: string }

    const file = new File(['{}'], 'data.json', { type: 'application/json' })
    const input = document.createElement('input')
    Object.defineProperty(input, 'files', { value: [file] })
    vm.onFileInput({ target: input } as unknown as Event)

    await flushPromises()
    expect(vm.parseError).toMatch(/yaml|yml/i)
  })

  it('rejects files larger than 1 MB', async () => {
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as { onFileInput: (e: Event) => void; parseError: string }

    vm.onFileInput(makeFileEvent('a: 1', 'fleet.yaml', 2_000_000))
    expect(vm.parseError).toMatch(/too large/i)
  })

  it('shows parse error for invalid YAML', async () => {
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      parseError: string
      step: string
    }

    const badFile = new File([': invalid: yaml: {[}'], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(badFile)
    await flushPromises()
    expect(vm.parseError).toMatch(/parse error|invalid/i)
  })

  it('shows parse error when top-level YAML is not an object', async () => {
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      parseError: string
    }

    const file = new File(['- item1\n- item2'], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()
    // Array is not a valid fleet file
    expect(vm.parseError).toBeTruthy()
  })

  // --- Diff computation ---

  it('marks new agents as create and existing as unchanged', async () => {
    const spaceWithAgent = {
      agents: { cto: { summary: '', status: 'idle', branch: '' } },
    }
    const wrapper = makeWrapper(spaceWithAgent)
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      agentDiffs: Array<{ name: string; action: string }>
      step: string
    }

    const file = new File([validFleetYaml], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()

    const ctoDiff = vm.agentDiffs.find(d => d.name === 'cto')
    const archDiff = vm.agentDiffs.find(d => d.name === 'arch')
    expect(ctoDiff?.action).toBe('unchanged')
    expect(archDiff?.action).toBe('create')
  })

  it('marks agents in space but not in YAML as orphan', async () => {
    const spaceWithOrphan = {
      agents: { qa: { summary: '', status: 'idle', branch: '' } },
    }
    const wrapper = makeWrapper(spaceWithOrphan)
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      agentDiffs: Array<{ name: string; action: string }>
    }

    const file = new File([validFleetYaml], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()

    const orphan = vm.agentDiffs.find(d => d.name === 'qa')
    expect(orphan?.action).toBe('orphan')
  })

  // --- XSS safety ---

  it('does not render user YAML content as HTML', async () => {
    const xssFleet = {
      version: '1',
      space: { name: 'Test Space' },
      agents: {
        'xss-agent': {
          role: '<script>alert(1)</script>',
          initial_prompt: '<img src=x onerror=alert(1)>',
        },
      },
    }
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      agentDiffs: Array<{ name: string; action: string }>
      step: string
    }

    const file = new File([yaml.dump(xssFleet)], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()

    // Check rendered HTML does not contain raw script tag
    expect(wrapper.html()).not.toContain('<script>')
    expect(wrapper.html()).not.toContain('onerror=')
  })

  // --- Happy path ---

  it('applies import: creates personas and agents in topo order', async () => {
    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      applyImport: () => Promise<void>
      step: string
      createdCount: number
    }

    const file = new File([validFleetYaml], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()
    expect(vm.step).toBe('diff')

    await vm.applyImport()
    await flushPromises()

    expect(vm.step).toBe('done')
    expect(vm.createdCount).toBe(2) // cto + arch

    // Persona created first
    expect(api.createPersona).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'Architecture Expert', prompt: 'You are an architect.' })
    )

    // cto created before arch (parent first)
    const createCalls = vi.mocked(api.createAgent).mock.calls
    const ctoIdx = createCalls.findIndex(c => c[1].name === 'cto')
    const archIdx = createCalls.findIndex(c => c[1].name === 'arch')
    expect(ctoIdx).toBeLessThan(archIdx)
  })

  // --- Space doesn't exist edge case ---

  it('shows success state even when space is empty (fresh import)', async () => {
    vi.mocked(api.createAgent).mockResolvedValue({ ok: true, agent: 'cto', backend: 'tmux', session: '', space: 'Test Space' })

    const wrapper = makeWrapper({ agents: {} })
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      applyImport: () => Promise<void>
      step: string
      dormantAgents: string[]
    }

    const file = new File([validFleetYaml], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()
    await vm.applyImport()
    await flushPromises()

    expect(vm.step).toBe('done')
    expect(vm.dormantAgents.length).toBeGreaterThan(0)
  })

  // --- Error handling ---

  it('shows error state and stays on diff step when apply fails', async () => {
    vi.mocked(api.createAgent).mockRejectedValueOnce(new Error('Server error 500'))

    const wrapper = makeWrapper()
    const vm = wrapper.vm as unknown as {
      processFile: (f: File) => void
      applyImport: () => Promise<void>
      step: string
      applyError: string
    }

    const file = new File([validFleetYaml], 'fleet.yaml', { type: 'application/yaml' })
    vm.processFile(file)
    await flushPromises()
    await vm.applyImport()
    await flushPromises()

    expect(vm.step).toBe('diff')
    expect(vm.applyError).toMatch(/Server error/i)
  })
})
