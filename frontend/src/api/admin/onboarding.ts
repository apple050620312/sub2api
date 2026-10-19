import { apiClient } from '../client'
import type { OnboardingConfig, OnboardingAdminOverview, OnboardingDiscordMember } from './onboardingTypes'
export type { OnboardingConfig, OnboardingAdminOverview } from './onboardingTypes'

export async function getOverview(): Promise<OnboardingAdminOverview> {
  const { data } = await apiClient.get<OnboardingAdminOverview>('/admin/onboarding')
  return data
}

export async function getConfig(): Promise<OnboardingConfig> {
  const { data } = await apiClient.get<OnboardingConfig>('/admin/onboarding/config')
  return data
}

export async function updateConfig(payload: Record<string, unknown>): Promise<OnboardingConfig> {
  const { data } = await apiClient.put<OnboardingConfig>('/admin/onboarding/config', payload)
  return data
}

export async function bindAccount(accountId: number, userId: number): Promise<void> {
  await apiClient.post(`/admin/onboarding/accounts/${accountId}/bind`, {
    user_id: userId,
  })
}

export async function unbindAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/admin/onboarding/accounts/${accountId}/bind`)
}

export async function getDiscordMembers(search = ''): Promise<OnboardingDiscordMember[]> {
  const { data } = await apiClient.get<OnboardingDiscordMember[]>('/admin/onboarding/discord/members', { params: { search } })
  return data
}

export async function bindDiscordMember(userId: number, discordUserId: string): Promise<void> {
  await apiClient.post('/admin/onboarding/discord/bind', { user_id: userId, discord_user_id: discordUserId })
}

export async function getDiscordChannels(): Promise<Array<{ id: string; name: string }>> {
  const { data } = await apiClient.get<Array<{ id: string; name: string }>>('/admin/onboarding/discord/channels')
  return data
}

export const onboardingAPI = { getOverview, getConfig, updateConfig, bindAccount, unbindAccount, getDiscordMembers, bindDiscordMember, getDiscordChannels }
export default onboardingAPI

