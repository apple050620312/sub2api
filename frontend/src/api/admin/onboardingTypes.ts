import type { OnboardingAccountSummary, OnboardingConfig, OnboardingDiscordBinding } from '../user'

export type { OnboardingConfig }

export interface OnboardingDiscordMember {
  id: string
  username: string
  display_name: string
  bound_email?: string
}

export interface OnboardingAdminUser {
  user_id: number
  email: string
  username: string
  discord?: OnboardingDiscordBinding
  accounts: OnboardingAccountSummary[]
  account_units: number
}

export interface OnboardingAdminOverview {
  config: OnboardingConfig
  users: OnboardingAdminUser[]
  unassigned_accounts: OnboardingAccountSummary[]
}

