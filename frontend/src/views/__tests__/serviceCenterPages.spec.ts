import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))

const { getUserStatus, getAdminOverview, getPressureConfig, setPressureConfig, setPolicy } = vi.hoisted(() => ({
  getUserStatus: vi.fn(),
  getAdminOverview: vi.fn(),
  getPressureConfig: vi.fn(),
  setPressureConfig: vi.fn(),
  setPolicy: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
vi.mock('@/api/serviceCenter', () => ({
  getDynamic5hPressure: getUserStatus,
  default: { getDynamic5hPressure: getUserStatus },
}))
vi.mock('@/api/admin/dashboard', () => ({
  getDynamic5hPressureOverview: getAdminOverview,
  getDynamic5hPressureConfig: getPressureConfig,
  setDynamic5hPressureConfig: setPressureConfig,
  resetDynamic5hUser: vi.fn(),
  setDynamic5hUserPolicy: setPolicy,
  default: { getDynamic5hPressureOverview: getAdminOverview },
}))

import UserServiceCenterView from '@/views/user/ServiceCenterView.vue'
import AdminServiceManagementView from '@/views/admin/ServiceManagementView.vue'

const global = {
  stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    ServiceSetupPanel: true,
    ServiceManagementPanel: true,
    Icon: true,
    LoadingSpinner: true,
  },
}

describe('dedicated Dynamic 5h pages', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getPressureConfig.mockResolvedValue({ peak_threshold: 1, normal_threshold: 0.9 })
  })

  it('lets administrators save pressure thresholds while enforcement is disabled', async () => {
    getAdminOverview.mockResolvedValue({ pool: { enabled: false }, users: [], accounts: [], audit_logs: [] })
    getPressureConfig.mockResolvedValue({ peak_threshold: 1.2, normal_threshold: 1.05 })
    setPressureConfig.mockResolvedValue({ peak_threshold: 1.3, normal_threshold: 1.1 })
    const wrapper = mount(AdminServiceManagementView, { global })
    await flushPromises()

    const inputs = wrapper.findAll('input[type="number"]')
    expect((inputs[0]!.element as HTMLInputElement).value).toBe('120')
    expect((inputs[1]!.element as HTMLInputElement).value).toBe('105')
    await inputs[0]!.setValue('130')
    await inputs[1]!.setValue('110')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(setPressureConfig).toHaveBeenCalledWith({ peak_threshold: 1.3, normal_threshold: 1.1 })
    wrapper.unmount()
  })

  it('rejects a leaving threshold above the entering threshold', async () => {
    getAdminOverview.mockResolvedValue({ pool: { enabled: false }, users: [], accounts: [], audit_logs: [] })
    const wrapper = mount(AdminServiceManagementView, { global })
    await flushPromises()
    await wrapper.findAll('input[type="number"]')[1]!.setValue('110')
    await wrapper.find('form').trigger('submit')
    expect(setPressureConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('preserves unsaved thresholds when the status refreshes', async () => {
    getAdminOverview.mockResolvedValue({ pool: { enabled: false }, users: [], accounts: [], audit_logs: [] })
    const wrapper = mount(AdminServiceManagementView, { global })
    await flushPromises()
    const peakInput = wrapper.findAll('input[type="number"]')[0]!
    await peakInput.setValue('125')
    await wrapper.find('header button').trigger('click')
    await flushPromises()
    expect((peakInput.element as HTMLInputElement).value).toBe('125')
    wrapper.unmount()
  })

  it('separates pool capacity from personal usage and distinguishes unknown windows', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true, quota_available: true, state: 'normal', currently_limited: false,
      usage_percent: 20, remaining_percent: 80,
      pool: {
        five_hour_used_percent: 40, five_hour_sample_count: 2,
        seven_day_sample_count: 0, stale: false,
        evaluated_at: '2026-10-20T00:00:00Z',
        accounts: [
          { name: '同名账号', platform: 'openai', included: true, reason: 'included', five_hour_used_percent: 0 },
          { name: '同名账号', platform: 'openai', included: false, reason: 'snapshot_missing' },
        ],
      },
    })
    const wrapper = mount(UserServiceCenterView, { global, attachTo: document.body })
    await flushPromises()
    const poolPanel = wrapper.findAll('[role="tabpanel"]')[1]!
    expect(poolPanel.isVisible()).toBe(false)
    await wrapper.findAll('[role="tab"]')[2]!.trigger('click')
    expect(poolPanel.isVisible()).toBe(true)
    expect(poolPanel.text()).toContain('60.0%')
    expect(poolPanel.text()).toContain('0 个样本')
    expect(poolPanel.text()).toContain('暂无窗口数据')
    expect(poolPanel.text()).toContain('窗口数据不完整')
    expect(poolPanel.findAll('tbody tr')).toHaveLength(2)
    expect(poolPanel.findAll('[role="progressbar"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('shows a user’s uncapped usage and zero remaining capacity', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true,
      quota_available: true,
      state: 'peak',
      limit_active: true,
      currently_limited: true,
      usage_percent: 180,
      remaining_percent: 0,
      window_started_at: '2026-09-18T00:00:00Z',
      recover_at: '2026-09-18T05:00:00Z',
    })

    const wrapper = mount(UserServiceCenterView, { global })
    await flushPromises()

    expect(wrapper.text()).toContain('dynamic5h.limited')
    expect(wrapper.text()).toContain('180.0%')
    expect(wrapper.text()).toContain('0.0%')
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('100')
    wrapper.unmount()
  })

  it('shows missing subscription allowance instead of a misleading zero', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true,
      state: 'normal',
      limit_active: false,
      currently_limited: false,
      usage_percent: 0,
      remaining_percent: 0,
    })

    const wrapper = mount(UserServiceCenterView, { global })
    await flushPromises()

    expect(wrapper.text()).toContain('dynamic5h.unavailable')
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('allows usage above 100% outside peak and keeps setup in the same page', async () => {
    getUserStatus.mockResolvedValueOnce({
      enabled: true,
      quota_available: true,
      state: 'normal',
      currently_limited: false,
      usage_percent: 140,
      remaining_percent: 0,
      usage_usd: 21,
      limit_usd: 15,
      seven_day_limit_usd: 100,
      window_started_at: '2026-09-18T00:00:00Z',
    })
    const wrapper = mount(UserServiceCenterView, { global, attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('dynamic5h.available')
    expect(wrapper.text()).not.toContain('dynamic5h.limited')
    expect(wrapper.text()).toContain('$21.00 / $15.00')
    expect(wrapper.find('service-setup-panel-stub').isVisible()).toBe(false)
    await wrapper.findAll('[role="tab"]')[0]!.trigger('click')
    expect(wrapper.find('service-setup-panel-stub').isVisible()).toBe(true)
    expect(wrapper.findAll('[role="tab"]')[0]!.attributes('aria-selected')).toBe('true')
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

    const wrapper = mount(AdminServiceManagementView, { global })
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('admin.pressure5hPage.users')
    expect(wrapper.findAll('[role="progressbar"]')).toHaveLength(3)
    wrapper.unmount()
  })

  it('keeps account pool usage out of the administrator page', async () => {
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

    const wrapper = mount(AdminServiceManagementView, { global })
    await flushPromises()

    expect(wrapper.text()).not.toContain('idle after reset')
    expect(wrapper.text()).not.toContain('admin.pressure5hPage.accountDiagnostics')
    const tabs = wrapper.findAll('[role="tab"]')
    expect(tabs.map(button => button.text())).toEqual(['dynamic5h.title', '用户与账号', 'Discord', '引导与订阅', '重置监测'])
    await tabs[4]!.trigger('click')
    expect(wrapper.find('service-management-panel-stub').attributes('panel')).toBe('reset')
    wrapper.unmount()
  })
})
