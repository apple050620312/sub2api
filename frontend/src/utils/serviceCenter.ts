import type { ApiKey, Group } from '@/types'
import { normalizePlanType, openAIPlanTypeLabel } from './planType'

export function serviceAccountPlanLabel(plan: string): string {
  switch (normalizePlanType(plan)) {
    case 'businessstandard':
    case 'team':
    case 'selfservebusinessusagebased': return 'Business Standard'
    case 'businesspremium':
    case 'selfservebusinessprolite': return 'Business Premium'
    case 'prolite':
    case 'pro5x': return 'Pro 5x'
    case 'pro':
    case 'chatgptpro': return 'Pro 20x'
    case 'promax': return 'Pro 50x'
    default: return openAIPlanTypeLabel(plan) || plan || '未知套餐'
  }
}

export function serviceKeyUnavailableReason(key: ApiKey, groups: Group[], now = Date.now()): string {
  if (key.status !== 'active') return { inactive: '已停用', expired: '已到期', quota_exhausted: '额度已用尽' }[key.status] || '不可用'
  if (key.expires_at && new Date(key.expires_at).getTime() <= now) return '已到期'
  if (key.quota > 0 && key.quota_used >= key.quota) return '额度已用尽'
  if (!groups.some(group => group.id === key.group_id && group.platform === 'openai' && group.status === 'active')) return '分组当前不可用'
  if (!key.key) return '无法读取密钥'
  return ''
}
