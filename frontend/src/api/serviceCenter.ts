import { apiClient } from './client'
import type { Dynamic5hUserStatus } from '@/types'

export interface ServiceAccountSummary {
  id: number
  name: string
  plan_type: string
  status: string
  email: string
  schedulable: boolean
  paused: boolean
  concurrency: number
  created_at: string
  updated_at: string
  last_used_at?: string
  expires_at?: string
  auth_expires_at?: string
  rate_limit_reset_at?: string
  blocked_until?: string
  groups: string[]
  usage: {
    five_hour_used_percent?: number
    five_hour_reset_at?: string
    seven_day_used_percent?: number
    seven_day_reset_at?: string
  }
}

export interface OnboardingDiscordBinding {
  user_id: number
  discord_user_id: string
  discord_username: string
  guild_id: string
  role_id: string
  status: string
  last_checked_at?: string
  created_at: string
  updated_at: string
}

export interface OnboardingDiscordConfig {
  client_id: string
  client_secret_configured: boolean
  bot_token_configured: boolean
  guild_id: string
  role_id: string
  redirect_uri: string
}

export interface ServiceCenterConfig {
  account_template_id?: number
  pool_reset?: { enabled: boolean; channel_id: string; message: string }
  enabled: boolean
  discord: OnboardingDiscordConfig
  subscription_rules: Record<string, { group_id: number; validity_days: number }>
  subscription_rules_need_review?: boolean
  openai_redirect_uri: string
  ccswitch_download_url: string
}

export interface ServiceCenterOverview {
  account_setup_message?: string
  discord?: OnboardingDiscordBinding
  discord_required: boolean
  accounts: ServiceAccountSummary[]
  account_units: number
  ready_for_account: boolean
  ready_for_key: boolean
  config: ServiceCenterConfig
  subscriptions: Array<{ group_name: string; status: string; expires_at: string }>
}

export async function getDynamic5hPressure(): Promise<Dynamic5hUserStatus> {
  const { data } = await apiClient.get<Dynamic5hUserStatus>('/user/5h-pressure')
  return data
}

export async function getServiceCenterOverview(): Promise<ServiceCenterOverview> {
  const { data } = await apiClient.get<ServiceCenterOverview>('/user/onboarding')
  return data
}

export async function setServiceAccountPaused(accountId: number, paused: boolean): Promise<void> {
  await apiClient.patch(`/user/onboarding/accounts/${accountId}`, { paused })
}

export async function deleteServiceAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/user/onboarding/accounts/${accountId}`)
}

export interface OnboardingOAuthStart {
  auth_url: string
  session_id: string
  onboarding_state: string
  redirect_uri: string
}

export async function startOpenAIOnboarding(redirectUri?: string): Promise<OnboardingOAuthStart> {
  const { data } = await apiClient.post<OnboardingOAuthStart>('/user/onboarding/openai/start', { redirect_uri: redirectUri })
  return data
}

export interface OnboardingOpenAICompleteRequest {
  session_id: string
  code: string
  state: string
  onboarding_state: string
  redirect_uri?: string
}

export async function startServiceAccountReauthorization(accountId: number): Promise<OnboardingOAuthStart> {
  const { data } = await apiClient.post<OnboardingOAuthStart>(`/user/onboarding/accounts/${accountId}/reauthorize/start`)
  return data
}

export async function completeServiceAccountReauthorization(accountId: number, payload: OnboardingOpenAICompleteRequest): Promise<{ account_id: number; name: string; plan_type: string }> {
  const { data } = await apiClient.post<{ account_id: number; name: string; plan_type: string }>(`/user/onboarding/accounts/${accountId}/reauthorize/complete`, payload)
  return data
}

export async function completeOpenAIOnboarding(payload: OnboardingOpenAICompleteRequest): Promise<{ account_id: number; name: string; plan_type: string }> {
  const { data } = await apiClient.post<{ account_id: number; name: string; plan_type: string }>('/user/onboarding/openai/complete', payload)
  return data
}
