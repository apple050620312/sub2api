import { apiClient } from '../client'
import type { OnboardingConfig, OnboardingAdminOverview } from './onboardingTypes'
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

export async function bindAccount(accountId: number, userId: number, publicFallback: boolean): Promise<void> {
  await apiClient.post(`/admin/onboarding/accounts/${accountId}/bind`, {
    user_id: userId,
    public_fallback: publicFallback,
  })
}

export async function unbindAccount(accountId: number): Promise<void> {
  await apiClient.delete(`/admin/onboarding/accounts/${accountId}/bind`)
}

export const onboardingAPI = { getOverview, getConfig, updateConfig, bindAccount, unbindAccount }
export default onboardingAPI

