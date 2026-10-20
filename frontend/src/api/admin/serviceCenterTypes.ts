import type { ServiceAccountSummary, ServiceCenterConfig, OnboardingDiscordBinding } from '../serviceCenter'

export type { ServiceCenterConfig }

export interface OnboardingDiscordMember {
  id: string
  username: string
  display_name: string
  bound_email?: string
}

export interface ServiceManagementUser {
  user_id: number
  email: string
  username: string
  discord?: OnboardingDiscordBinding
  accounts: ServiceAccountSummary[]
  account_units: number
}

export interface ServiceManagementOverview {
  config: ServiceCenterConfig
  users: ServiceManagementUser[]
  unassigned_accounts: ServiceAccountSummary[]
  template_accounts?: ServiceAccountSummary[]
  pool_reset_status?: {
    checked_at?: string
    phase: string
    last_error?: string
    pending_until?: string
    cooldown_until?: string
    last_reset_at?: string
    last_reset_kind?: string
    affected_subscriptions: number
    notification_state?: string
  }
}

