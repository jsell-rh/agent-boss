import type { ClassValue } from "clsx"
import { clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/** Build a full PR URL from agent.pr + agent.repo_url, or return null. */
export function prLink(agent: { pr?: string; repo_url?: string }): string | null {
  if (!agent.pr) return null
  if (agent.pr.startsWith('http')) return agent.pr
  if (!agent.repo_url) return null
  const repoBase = agent.repo_url.replace(/\.git$/, '').replace(/\/$/, '')
  const prNum = agent.pr.replace(/^#/, '')
  return `${repoBase}/pull/${prNum}`
}
