/**
 * User API endpoints
 * Handles user profile management and password changes
 */

import { apiClient } from './client'
import {
  resolveWeChatOAuthStartStrict,
  prepareOAuthBindAccessTokenCookie,
  type WeChatOAuthPublicSettings,
} from './auth'
import type {
  User,
  ChangePasswordRequest,
  NotifyEmailEntry,
  UserAuthProvider,
  UserAffiliateDetail,
  AffiliateTransferResponse,
  PlatformQuotasResponse,
  Dynamic5hUserStatus,
} from '@/types'

export interface OnboardingAccountSummary {
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

export interface OnboardingConfig {
  account_template_id?: number
  pool_reset?: { enabled: boolean; channel_id: string; message: string }
  enabled: boolean
  discord: OnboardingDiscordConfig
  subscription_rules: Record<string, { group_id: number; validity_days: number }>
  subscription_rules_need_review?: boolean
  openai_redirect_uri: string
  ccswitch_download_url: string
}

export interface OnboardingUserOverview {
  account_setup_message?: string
  discord?: OnboardingDiscordBinding
  discord_required: boolean
  accounts: OnboardingAccountSummary[]
  account_units: number
  ready_for_account: boolean
  ready_for_key: boolean
  config: OnboardingConfig
  subscriptions: Array<{ group_name: string; status: string; expires_at: string }>
}

/**
 * Get current user profile
 * @returns User profile data
 */
export async function getProfile(): Promise<User> {
  const { data } = await apiClient.get<User>('/user/profile')
  return data
}

/**
 * Update current user profile
 * @param profile - Profile data to update
 * @returns Updated user profile data
 */
export async function updateProfile(profile: {
  username?: string
  avatar_url?: string | null
  balance_notify_enabled?: boolean
  balance_notify_threshold?: number | null
  balance_notify_extra_emails?: NotifyEmailEntry[]
}): Promise<User> {
  const { data } = await apiClient.put<User>('/user', profile)
  return data
}

/**
 * Change current user password
 * @param passwords - Old and new password
 * @returns Success message
 */
export async function changePassword(
  oldPassword: string,
  newPassword: string
): Promise<{ message: string }> {
  const payload: ChangePasswordRequest = {
    old_password: oldPassword,
    new_password: newPassword
  }

  const { data } = await apiClient.put<{ message: string }>('/user/password', payload)
  return data
}

/**
 * Send verification code for adding a notify email
 * @param email - Email address to verify
 */
export async function sendNotifyEmailCode(email: string): Promise<void> {
  await apiClient.post('/user/notify-email/send-code', { email })
}

/**
 * Verify and add a notify email
 * @param email - Email address to add
 * @param code - Verification code
 */
export async function verifyNotifyEmail(email: string, code: string): Promise<void> {
  await apiClient.post('/user/notify-email/verify', { email, code })
}

/**
 * Remove a notify email
 * @param email - Email address to remove
 */
export async function removeNotifyEmail(email: string): Promise<void> {
  await apiClient.delete('/user/notify-email', { data: { email } })
}

/**
 * Toggle a notify email's disabled state
 * @param email - Email address (empty string for primary email placeholder)
 * @param disabled - Whether to disable the email
 */
export async function toggleNotifyEmail(email: string, disabled: boolean): Promise<User> {
  const { data } = await apiClient.put<User>('/user/notify-email/toggle', { email, disabled })
  return data
}

export async function sendEmailBindingCode(email: string): Promise<void> {
  await apiClient.post('/user/account-bindings/email/send-code', { email })
}

export async function bindEmailIdentity(payload: {
  email: string
  verify_code: string
  password: string
}): Promise<User> {
  const { data } = await apiClient.post<User>('/user/account-bindings/email', payload)
  return data
}

export async function unbindAuthIdentity(provider: BindableOAuthProvider): Promise<User> {
  const { data } = await apiClient.delete<User>(`/user/account-bindings/${provider}`)
  return data
}

export type BindableOAuthProvider = Exclude<UserAuthProvider, 'email'>

interface BuildOAuthBindingStartURLOptions {
  redirectTo?: string
  wechatOAuthSettings?: WeChatOAuthPublicSettings | null
}

export function resolveWeChatOAuthMode(): 'open' | 'mp' {
  if (typeof navigator === 'undefined') {
    return 'open'
  }
  return /MicroMessenger/i.test(navigator.userAgent) ? 'mp' : 'open'
}

function resolveWeChatOAuthBindingMode(
  settings?: WeChatOAuthPublicSettings | null
): 'open' | 'mp' | null {
  if (settings) {
    return resolveWeChatOAuthStartStrict(settings).mode
  }
  return resolveWeChatOAuthMode()
}

export function buildOAuthBindingStartURL(
  provider: BindableOAuthProvider,
  options: BuildOAuthBindingStartURLOptions = {}
): string | null {
  const redirectTo = options.redirectTo?.trim() || '/profile'
  const apiBase = (import.meta.env.VITE_API_BASE_URL as string | undefined) || '/api/v1'
  const normalized = apiBase.replace(/\/$/, '')
  const params = new URLSearchParams({
    redirect: redirectTo,
    intent: 'bind_current_user'
  })

  if (provider === 'wechat') {
    const mode = resolveWeChatOAuthBindingMode(options.wechatOAuthSettings)
    if (!mode) {
      return null
    }
    params.set('mode', mode)
  }

  return `${normalized}/auth/oauth/${provider}/bind/start?${params.toString()}`
}

export async function startOAuthBinding(
  provider: BindableOAuthProvider,
  options: BuildOAuthBindingStartURLOptions = {}
): Promise<void> {
  if (typeof window === 'undefined') {
    return
  }
  const startURL = buildOAuthBindingStartURL(provider, options)
  if (!startURL) {
    return
  }
  await prepareOAuthBindAccessTokenCookie()
  window.location.href = startURL
}

export async function getAffiliateDetail(): Promise<UserAffiliateDetail> {
  const { data } = await apiClient.get<UserAffiliateDetail>('/user/aff')
  return data
}

export async function transferAffiliateQuota(): Promise<AffiliateTransferResponse> {
  const { data } = await apiClient.post<AffiliateTransferResponse>('/user/aff/transfer')
  return data
}

/**
 * 获取当前用户的平台限额 + 用量。
 */
export async function getMyPlatformQuotas(): Promise<PlatformQuotasResponse> {
  const { data } = await apiClient.get<PlatformQuotasResponse>('/user/platform-quotas')
  return data
}

export async function getDynamic5hPressure(): Promise<Dynamic5hUserStatus> {
  const { data } = await apiClient.get<Dynamic5hUserStatus>('/user/5h-pressure')
  return data
}

export async function getOnboardingOverview(): Promise<OnboardingUserOverview> {
  const { data } = await apiClient.get<OnboardingUserOverview>('/user/onboarding')
  return data
}

export async function setOnboardingAccountPaused(accountId: number, paused: boolean): Promise<void> {
  await apiClient.patch(`/user/onboarding/accounts/${accountId}`, { paused })
}

export async function deleteOnboardingAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/user/onboarding/accounts/${accountId}`)
}

export async function startDiscordOnboarding(redirectUri?: string): Promise<{ auth_url: string }> {
  const { data } = await apiClient.post<{ auth_url: string }>('/user/onboarding/discord/start', { redirect_uri: redirectUri })
  return data
}

export async function completeDiscordOnboarding(payload: { code: string; state: string; redirect_uri?: string }): Promise<OnboardingDiscordBinding> {
  const { data } = await apiClient.post<OnboardingDiscordBinding>('/user/onboarding/discord/complete', payload)
  return data
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

export async function completeOpenAIOnboarding(payload: {
  session_id: string
  code: string
  state: string
  onboarding_state: string
  redirect_uri?: string
}): Promise<{ account_id: number; name: string; plan_type: string }> {
  const { data } = await apiClient.post<{ account_id: number; name: string; plan_type: string }>('/user/onboarding/openai/complete', payload)
  return data
}

export const userAPI = {
  getProfile,
  updateProfile,
  changePassword,
  sendNotifyEmailCode,
  verifyNotifyEmail,
  removeNotifyEmail,
  toggleNotifyEmail,
  sendEmailBindingCode,
  bindEmailIdentity,
  unbindAuthIdentity,
  buildOAuthBindingStartURL,
  startOAuthBinding,
  getAffiliateDetail,
  transferAffiliateQuota,
  getMyPlatformQuotas,
  getDynamic5hPressure,
  getOnboardingOverview,
  startDiscordOnboarding,
  completeDiscordOnboarding,
  startOpenAIOnboarding,
  completeOpenAIOnboarding,
}

export default userAPI
