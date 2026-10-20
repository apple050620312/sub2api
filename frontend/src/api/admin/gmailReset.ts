import { apiClient } from '../client'

export interface GmailResetConfig {
  senders: string[]
  query: string
  redirect_uri: string
  poll_minutes: number
  propagation_minutes: number
  aggregation_minutes: number
  cooldown_minutes: number
  max_age_hours: number
  batch_size: number
  max_model_attempts: number
  retention_days: number
  require_authenticated_sender: boolean
  direct_reset_enabled: boolean
  banked_reset_enabled: boolean
  min_confidence: number
  model_base_url: string
  model: string
  api_format: 'chat_completions' | 'responses'
  prompt: string
  model_timeout_seconds: number
  max_output_tokens: number
  max_body_chars: number
}

export interface GmailResetOverview {
  config: GmailResetConfig
  credentials_configured: boolean
  model_key_configured: boolean
  connected: boolean
  email: string
  monitor_since: string
  recent_mail: Array<{
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

const path = '/admin/onboarding/gmail-reset'
export const gmailResetAPI = {
  async get() { return (await apiClient.get<GmailResetOverview>(path)).data },
  async save(config: GmailResetConfig, credentialsJSON: string, modelAPIKey: string) {
    return (await apiClient.put<GmailResetOverview>(path, { config, credentials_json: credentialsJSON, model_api_key: modelAPIKey })).data
  },
  async start() { return (await apiClient.post<{ auth_url: string }>(`${path}/oauth/start`)).data },
  async complete(code: string, state: string) { await apiClient.post(`${path}/oauth/complete`, { code, state }) },
  async disconnect() { await apiClient.delete(`${path}/oauth`) },
}
