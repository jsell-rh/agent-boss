import { describe, it, expect } from 'vitest'
import { prLink } from '@/lib/utils'

describe('prLink', () => {
  it('returns null when pr is missing', () => {
    expect(prLink({ pr: undefined, repo_url: 'https://github.com/org/repo' })).toBeNull()
  })

  it('returns null when repo_url is missing and pr is not a full URL', () => {
    expect(prLink({ pr: '#42', repo_url: undefined })).toBeNull()
  })

  it('returns pr directly when it is already a full URL', () => {
    const url = 'https://github.com/org/repo/pull/42'
    expect(prLink({ pr: url })).toBe(url)
  })

  it('builds full URL from repo_url + pr number with hash', () => {
    const result = prLink({ pr: '#42', repo_url: 'https://github.com/org/repo' })
    expect(result).toBe('https://github.com/org/repo/pull/42')
  })

  it('strips .git suffix from repo_url', () => {
    const result = prLink({ pr: '#7', repo_url: 'https://github.com/org/repo.git' })
    expect(result).toBe('https://github.com/org/repo/pull/7')
  })

  it('strips trailing slash from repo_url', () => {
    const result = prLink({ pr: '#3', repo_url: 'https://github.com/org/repo/' })
    expect(result).toBe('https://github.com/org/repo/pull/3')
  })

  it('handles pr number without hash prefix', () => {
    const result = prLink({ pr: '99', repo_url: 'https://github.com/org/repo' })
    expect(result).toBe('https://github.com/org/repo/pull/99')
  })
})
