import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatBytes, pad2, timeAgo } from '@/lib/format'

describe('formatBytes', () => {
  it('renders a placeholder for zero or negative sizes', () => {
    expect(formatBytes(0)).toBe('—')
    expect(formatBytes(-1)).toBe('—')
  })

  it('keeps sub-KB values in whole bytes', () => {
    expect(formatBytes(1)).toBe('1 B')
    expect(formatBytes(1023)).toBe('1023 B')
  })

  it('scales through the units with one decimal', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(1.4 * 1024 ** 3)).toBe('1.4 GB')
  })

  it('drops the decimal once the value reaches three digits', () => {
    expect(formatBytes(100 * 1024 ** 2)).toBe('100 MB')
  })

  it('caps at TB instead of inventing a larger unit', () => {
    expect(formatBytes(1024 ** 5)).toBe('1024 TB')
  })
})

describe('timeAgo', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  const freeze = (iso: string) => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(iso))
  }

  it('treats a bare SQLite timestamp as UTC', () => {
    freeze('2026-07-23T12:00:00Z')
    expect(timeAgo('2026-07-23 10:00:00')).toBe('2h ago')
  })

  it('steps through the coarser units', () => {
    freeze('2026-07-23T12:00:00Z')
    expect(timeAgo('2026-07-23T11:59:30Z')).toBe('just now')
    expect(timeAgo('2026-07-23T11:15:00Z')).toBe('45m ago')
    expect(timeAgo('2026-07-20T12:00:00Z')).toBe('3d ago')
    expect(timeAgo('2026-05-23T12:00:00Z')).toBe('2mo ago')
    expect(timeAgo('2024-07-23T12:00:00Z')).toBe('2y ago')
  })

  it('clamps future timestamps to just now', () => {
    freeze('2026-07-23T12:00:00Z')
    expect(timeAgo('2026-07-23T13:00:00Z')).toBe('just now')
  })

  it('returns unparseable input unchanged', () => {
    expect(timeAgo('not a timestamp')).toBe('not a timestamp')
  })
})

describe('pad2', () => {
  it('zero-pads single digits', () => {
    expect(pad2(0)).toBe('00')
    expect(pad2(5)).toBe('05')
  })

  it('leaves two or more digits alone', () => {
    expect(pad2(12)).toBe('12')
    expect(pad2(112)).toBe('112')
  })
})
