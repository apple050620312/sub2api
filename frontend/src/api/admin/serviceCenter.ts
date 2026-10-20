import { apiClient } from '../client'
import type { ServiceCenterConfig, ServiceManagementOverview, OnboardingDiscordMember } from './serviceCenterTypes'
export type { ServiceCenterConfig, ServiceManagementOverview } from './serviceCenterTypes'

export async function getOverview(): Promise<ServiceManagementOverview> {
  const { data } = await apiClient.get<ServiceManagementOverview>('/admin/onboarding')
  return data
}

export async function getConfig(): Promise<ServiceCenterConfig> {
  const { data } = await apiClient.get<ServiceCenterConfig>('/admin/onboarding/config')
  return data
}

export async function updateConfig(payload: Record<string, unknown>): Promise<ServiceCenterConfig> {
  const { data } = await apiClient.put<ServiceCenterConfig>('/admin/onboarding/config', payload)
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

export const serviceCenterAPI = { getOverview, getConfig, updateConfig, bindAccount, unbindAccount, getDiscordMembers, bindDiscordMember, getDiscordChannels }
export default serviceCenterAPI

