import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { getUserStatus, getAdminOverview } = vi.hoisted(() => ({
  getUserStatus: vi.fn(),
  getAdminOverview: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
vi.mock('@/api/user', () => ({
  getDynamic5hPressure: getUserStatus,
  default: { getDynamic5hPressure: getUserStatus },
}))
vi.mock('@/api/admin/dashboard', () => ({
  getDynamic5hPressureOverview: getAdminOverview,
  resetDynamic5hUser: vi.fn(),
  setDynamic5hUserPolicy: vi.fn(),
  default: { getDynamic5hPressureOverview: getAdminOverview },
}))

import UserDynamic5hPressureView from '@/views/user/Dynamic5hPressureView.vue'
import AdminDynamic5hPressureView from '@/views/admin/Dynamic5hPressureView.vue'

const global = {
  stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    Icon: true,
    LoadingSpinner: true,
  },
}

describe('dedicated Dynamic 5h pages', () => {
  it('shows a user’s uncapped usage and zero remaining capacity', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true,
      state: 'peak',
      limit_active: true,
      currently_limited: true,
      usage_percent: 180,
      remaining_percent: 0,
      window_started_at: '2026-09-18T00:00:00Z',
      recover_at: '2026-09-18T05:00:00Z',
    })

    const wrapper = mount(UserDynamic5hPressureView, { global })
    await flushPromises()

    expect(wrapper.text()).toContain('dynamic5h.limited')
    expect(wrapper.text()).toContain('180.0%')
    expect(wrapper.text()).toContain('0.0%')
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('100')
    wrapper.unmount()
  })

  it('shows calibration unavailable instead of a misleading zero', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true,
      state: 'normal',
      limit_active: false,
      currently_limited: false,
      usage_percent: 0,
      remaining_percent: 0,
    })

    const wrapper = mount(UserDynamic5hPressureView, { global })
    await flushPromises()

    expect(wrapper.text()).toContain('dynamic5h.unavailable')
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders every active user in the administrator overview', async () => {
    getAdminOverview.mockResolvedValueOnce({
      pool: {
        enabled: true,
        data_available: true,
        calibration_ready: true,
        pressure: 0.9,
        raw_pressure: 0.9,
        state: 'peak',
        peak_active: true,
        account_count: 4,
        active_user_count: 3,
        remaining_capacity: 1,
        burn_rate_per_hour: 1,
        projected_demand: 4,
        pool_capacity: 400,
        capacity_recovering_next_hour: 0,
        excluded_account_count: 0,
        peak_threshold: 0.85,
        normal_threshold: 0.7,
        evaluated_at: '2026-09-18T00:00:00Z',
      },
      users: [
        { user_id: 1, enabled: true, state: 'peak', limit_active: true, currently_limited: true, usage_percent: 180, remaining_percent: 0, window_started_at: '2026-09-18T00:00:00Z' },
        { user_id: 3, enabled: true, state: 'peak', limit_active: true, currently_limited: false, usage_percent: 60, remaining_percent: 40, window_started_at: '2026-09-18T00:00:00Z' },
        { user_id: 2, enabled: true, state: 'peak', limit_active: true, currently_limited: false, usage_percent: 20, remaining_percent: 80, window_started_at: '2026-09-18T00:00:00Z' },
      ],
      accounts: [],
      audit_logs: [],
    })

    const wrapper = mount(AdminDynamic5hPressureView, { global })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('用户 #1')
    expect(text).toContain('用户 #2')
    expect(text).toContain('用户 #3')
    expect(text.indexOf('用户 #1')).toBeLessThan(text.indexOf('用户 #3'))
    expect(text.indexOf('用户 #3')).toBeLessThan(text.indexOf('用户 #2'))
    expect(wrapper.findAll('[role="progressbar"]')).toHaveLength(3)
    wrapper.unmount()
  })

  it('shows a reset and unused account as available now', async () => {
    getAdminOverview.mockResolvedValueOnce({
      pool: {
        enabled: true,
        data_available: true,
        calibration_ready: true,
        pressure: 0.2,
        raw_pressure: 0.2,
        state: 'normal',
        peak_active: false,
        account_count: 1,
        active_user_count: 0,
        remaining_capacity: 1,
        burn_rate_per_hour: 0,
        projected_demand: 0,
        pool_capacity: 100,
        capacity_recovering_next_hour: 0,
        excluded_account_count: 0,
        peak_threshold: 0.85,
        normal_threshold: 0.7,
        evaluated_at: '2026-09-18T05:00:00Z',
      },
      users: [],
      accounts: [{
        account_id: 42,
        name: 'idle after reset',
        platform: 'openai',
        included: true,
        reason: 'included',
        five_hour_used_percent: 0,
      }],
      audit_logs: [],
    })

    const wrapper = mount(AdminDynamic5hPressureView, { global })
    await flushPromises()

    expect(wrapper.text()).toContain('0.0% · common.now')
    expect(wrapper.text()).not.toContain('admin.pressure5hPage.accountReasons.snapshot_expired')
    wrapper.unmount()
  })
})
