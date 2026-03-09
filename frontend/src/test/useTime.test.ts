import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { relativeTime, formatWaitTime, freshness } from '@/composables/useTime'

describe('relativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns "just now" for future dates', () => {
    const future = new Date(Date.now() + 5000).toISOString()
    expect(relativeTime(future)).toBe('just now')
  })

  it('returns seconds ago for < 60s', () => {
    const past = new Date(Date.now() - 30_000).toISOString()
    expect(relativeTime(past)).toBe('30s ago')
  })

  it('returns minutes ago for < 60m', () => {
    const past = new Date(Date.now() - 5 * 60_000).toISOString()
    expect(relativeTime(past)).toBe('5m ago')
  })

  it('returns hours ago for < 24h', () => {
    const past = new Date(Date.now() - 3 * 3_600_000).toISOString()
    expect(relativeTime(past)).toBe('3h ago')
  })

  it('returns days ago for >= 24h', () => {
    const past = new Date(Date.now() - 2 * 86_400_000).toISOString()
    expect(relativeTime(past)).toBe('2d ago')
  })
})

describe('formatWaitTime', () => {
  it('shows seconds for < 60', () => {
    expect(formatWaitTime(45)).toBe('45s')
  })

  it('shows minutes for < 3600', () => {
    expect(formatWaitTime(90)).toBe('2m')
  })

  it('shows hours for >= 3600', () => {
    expect(formatWaitTime(7200)).toBe('2.0h')
  })
})

describe('freshness', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns "live" for < 1 min ago', () => {
    const ts = new Date(Date.now() - 30_000).toISOString()
    expect(freshness(ts)).toBe('live')
  })

  it('returns "recent" for < 5 min ago', () => {
    const ts = new Date(Date.now() - 3 * 60_000).toISOString()
    expect(freshness(ts)).toBe('recent')
  })

  it('returns "stale" for > 30 min ago', () => {
    const ts = new Date(Date.now() - 60 * 60_000).toISOString()
    expect(freshness(ts)).toBe('stale')
  })
})
