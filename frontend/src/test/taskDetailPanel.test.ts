import { describe, it, expect } from 'vitest'
import type { Task, AgentUpdate } from '@/types'

// Replicate the buildPrUrl logic from TaskDetailPanel for isolated testing

function buildPrUrl(
  pr: string,
  task: Pick<Task, 'assigned_to'>,
  agents: Record<string, Pick<AgentUpdate, 'repo_url'>>,
): string | null {
  if (pr.startsWith('http')) return pr
  if (task.assigned_to) {
    const agent = agents[task.assigned_to]
    if (agent?.repo_url) {
      const base = agent.repo_url.replace(/\.git$/, '').replace(/\/$/, '')
      const num = pr.replace(/^#/, '')
      return `${base}/pull/${num}`
    }
  }
  for (const agent of Object.values(agents)) {
    if (agent.repo_url) {
      const base = agent.repo_url.replace(/\.git$/, '').replace(/\/$/, '')
      const num = pr.replace(/^#/, '')
      return `${base}/pull/${num}`
    }
  }
  return null
}

describe('TaskDetailPanel buildPrUrl', () => {
  const agents = {
    Alice: { repo_url: 'https://github.com/org/repo' },
    Bob: { repo_url: undefined },
  }

  it('returns full URL unchanged when pr is already http', () => {
    const url = 'https://github.com/org/repo/pull/42'
    expect(buildPrUrl(url, { assigned_to: 'Alice' }, agents)).toBe(url)
  })

  it('builds URL from assigned agent repo_url', () => {
    expect(buildPrUrl('#5', { assigned_to: 'Alice' }, agents)).toBe(
      'https://github.com/org/repo/pull/5',
    )
  })

  it('falls back to any agent with repo_url when assigned agent has none', () => {
    expect(buildPrUrl('#10', { assigned_to: 'Bob' }, agents)).toBe(
      'https://github.com/org/repo/pull/10',
    )
  })

  it('returns null when no agent has repo_url', () => {
    expect(buildPrUrl('#1', { assigned_to: 'Charlie' }, { Charlie: {} })).toBeNull()
  })

  it('returns null when agents map is empty', () => {
    expect(buildPrUrl('#1', { assigned_to: undefined }, {})).toBeNull()
  })

  it('strips # prefix from pr number', () => {
    expect(buildPrUrl('#99', { assigned_to: 'Alice' }, agents)).toBe(
      'https://github.com/org/repo/pull/99',
    )
  })

  it('handles pr number without # prefix', () => {
    expect(buildPrUrl('42', { assigned_to: 'Alice' }, agents)).toBe(
      'https://github.com/org/repo/pull/42',
    )
  })
})
