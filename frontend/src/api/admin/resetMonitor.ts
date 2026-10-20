import { apiClient } from '../client'

export interface ResetMonitorConfig {
  channel_id: string
  author_ids: string[]
  poll_seconds: number
  propagation_minutes: number
  cooldown_minutes: number
  banked_reset_enabled: boolean
  model_api_key_id: number
  model: string
  api_format: 'chat_completions' | 'responses'
  prompt: string
}

export interface ResetMonitorOverview {
  config: ResetMonitorConfig
  monitor_since: string
  recent_messages: Array<{
    message_id: string
    received_at: string
    sender: string
    subject: string
    kind: string
    confidence: number
    evidence: string
    outcome: string
    attempts: number
  }>
}

const path = '/admin/onboarding/reset-monitor'
export const resetMonitorAPI = {
  async get() { return (await apiClient.get<ResetMonitorOverview>(path)).data },
  async save(config: ResetMonitorConfig) { return (await apiClient.put<ResetMonitorOverview>(path, { config })).data },
}
