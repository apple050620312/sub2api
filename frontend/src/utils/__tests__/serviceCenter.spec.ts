import { describe, expect, it } from 'vitest'
import type { ApiKey, Group } from '@/types'
import { serviceAccountPlanLabel, serviceKeyUnavailableReason } from '../serviceCenter'

describe('service center identity labels', () => {
  it.each([
    ['team', 'Business Standard'],
    ['self_serve_business_usage_based', 'Business Standard'],
    ['business_standard', 'Business Standard'],
    ['self_serve_business_prolite', 'Business Premium'],
    ['business_premium', 'Business Premium'],
    ['prolite', 'Pro 5x'],
    ['pro_5x', 'Pro 5x'],
  ])('labels %s as %s', (plan, label) => {
    expect(serviceAccountPlanLabel(plan)).toBe(label)
  })
})

describe('service center existing key selection', () => {
  const key = { status: 'active', group_id: 9, key: 'sk-existing', quota: 0, quota_used: 0 } as ApiKey
  const groups = [{ id: 9, platform: 'openai', status: 'active' }] as Group[]

  it('accepts an active key in an accessible OpenAI group', () => {
    expect(serviceKeyUnavailableReason(key, groups)).toBe('')
  })

  it('rejects a key whose subscription group is no longer available', () => {
    expect(serviceKeyUnavailableReason(key, [])).toBe('分组当前不可用')
    expect(serviceKeyUnavailableReason(key, [{ ...groups[0]!, platform: 'anthropic' }])).toBe('分组当前不可用')
  })

  it('rejects the exact expiry and quota boundaries', () => {
    expect(serviceKeyUnavailableReason({ ...key, expires_at: '2026-10-19T00:00:00Z' }, groups, Date.parse('2026-10-19T00:00:00Z'))).toBe('已到期')
    expect(serviceKeyUnavailableReason({ ...key, quota: 10, quota_used: 10 }, groups)).toBe('额度已用尽')
  })
})
