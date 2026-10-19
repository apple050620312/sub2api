import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const api = vi.hoisted(() => ({ userOverview: vi.fn(), adminOverview: vi.fn(), pause: vi.fn(), remove: vi.fn(), bindAccount: vi.fn(), bindDiscord: vi.fn(), members: vi.fn(), channels: vi.fn(), showError: vi.fn(), listKeys: vi.fn(), createKey: vi.fn(), updateConfig: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.showError, showSuccess: vi.fn() }) }))
vi.mock('@/api/user', () => ({ getOnboardingOverview: api.userOverview, setOnboardingAccountPaused: api.pause, deleteOnboardingAccount: api.remove }))
vi.mock('@/api/admin/onboarding', () => ({ onboardingAPI: { getOverview: api.adminOverview, bindAccount: api.bindAccount, bindDiscordMember: api.bindDiscord, getDiscordMembers: api.members, getDiscordChannels: api.channels, updateConfig: api.updateConfig } }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn().mockResolvedValue([{ id: 9, name: 'Plus 周额度', platform: 'openai', status: 'active', subscription_type: 'subscription' }]) }))
vi.mock('@/api/groups', () => ({ userGroupsAPI: { getAvailable: vi.fn().mockResolvedValue([{ id: 9, name: 'Plus 周额度', platform: 'openai', status: 'active', subscription_type: 'subscription' }]) } }))
vi.mock('@/api/keys', () => ({ keysAPI: { create: api.createKey, list: api.listKeys } }))

import UserOnboardingView from '@/views/user/OnboardingView.vue'
import AdminOnboardingView from '@/views/admin/OnboardingView.vue'

const account = { id: 42, name: '共享 GPT Plus', email: 'gpt@example.com', plan_type: 'plus', status: 'active', paused: false, schedulable: true, concurrency: 3, groups: ['Plus 周额度'], usage: { five_hour_used_percent: 25 }, created_at: '2026-10-18T00:00:00Z', updated_at: '2026-10-18T00:00:00Z' }
const config = { enabled: true, discord: { client_id: '', client_secret_configured: true, bot_token_configured: true, guild_id: 'guild', role_id: 'role', redirect_uri: '' }, subscription_rules: {}, openai_redirect_uri: '', ccswitch_download_url: '' }
const existingKey = { id: 88, name: '我的现有 Codex', key: 'sk-existing-private', group_id: 9, group: { id: 9, name: 'Plus 周额度', platform: 'openai' }, status: 'active', expires_at: null, quota: 0, quota_used: 0 }
const global = { stubs: { ServiceAccountDetails: true, BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, ConfirmDialog: { props: ['show', 'title', 'message'], template: '<div v-if="show">{{ title }} {{ message }}</div>' } } }

beforeEach(() => {
  vi.clearAllMocks()
  api.userOverview.mockResolvedValue({ accounts: [account], account_units: 1, subscriptions: [], ready_for_key: true, ready_for_account: true, config })
  api.adminOverview.mockResolvedValue({ config, users: [{ user_id: 7, email: 'owner@example.com', username: 'Owner', accounts: [], account_units: 0 }], unassigned_accounts: [account] })
  api.members.mockResolvedValue([{ id: '12345', username: 'discord-user', display_name: 'Discord 昵称' }])
  api.channels.mockResolvedValue([{ id: '987654', name: '额度通知' }])
  api.listKeys.mockResolvedValue({ items: [], total: 0 })
  api.updateConfig.mockResolvedValue(config)
})

describe('service center account management', () => {
  it('confirms a named template and named notification channel before saving', async () => {
    api.adminOverview.mockResolvedValueOnce({ config: { ...config, account_template_id: 42, pool_reset: { enabled: true, channel_id: '987654', message: '全员配额已恢复' } }, users: [], unassigned_accounts: [], template_accounts: [account] })
    const wrapper = mount(AdminOnboardingView, { global })
    await flushPromises()
    const selector = wrapper.findAllComponents(Select).find(select => select.props('ariaLabel') === '指定模板账号')!
    expect(selector.props('options')).toContainEqual({ value: 42, label: '共享 GPT Plus · gpt@example.com' })
    await wrapper.findAll('button').find(button => button.text() === '保存配置')!.trigger('click')
    expect(api.updateConfig).not.toHaveBeenCalled()
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('共享 GPT Plus · gpt@example.com')
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('#额度通知')
    expect(wrapper.findComponent(ConfirmDialog).props('message')).not.toContain('987654')
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ account_template_id: 42, pool_reset: { enabled: true, channel_id: '987654', message: '全员配额已恢复' } }))
    wrapper.unmount()
  })

  it('explains that account onboarding needs administrator configuration after Discord is bound', async () => {
    api.userOverview.mockResolvedValueOnce({ accounts: [], subscriptions: [], discord: { status: 'active', discord_username: '测试成员' }, account_units: 0, ready_for_key: false, ready_for_account: false, account_setup_message: '管理员尚未指定 GPT 模板账号。', config })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    expect(wrapper.text()).toContain('管理员尚未指定 GPT 模板账号。')
    expect(wrapper.findAll('button').find(button => button.text() === '打开 OpenAI 授权')!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('completes step three using an existing key and imports it in step four', async () => {
    api.listKeys.mockResolvedValue({ items: [existingKey], total: 1 })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    const keyStep = wrapper.find('[aria-label="第三步：API Key"]')
    expect(keyStep.text()).toContain('已完成')
    expect(keyStep.text()).toContain('我的现有 Codex')
    expect(keyStep.text()).toContain('Plus 周额度')
    expect(keyStep.text()).not.toContain('sk-existing-private')
    const importLink = wrapper.find('[aria-label="第四步：CC Switch"] a[href^="ccswitch://"]')
    expect(new URL(importLink.attributes('href')).searchParams.get('apiKey')).toBe(existingKey.key)
    expect(new URL(importLink.attributes('href')).searchParams.get('app')).toBe('codex')
    expect(api.createKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('imports the selected key by name without showing numeric IDs', async () => {
    api.listKeys.mockResolvedValue({ items: [existingKey, { ...existingKey, id: 89, name: '我的第二个 Codex', key: 'sk-second' }], total: 2 })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    wrapper.findAllComponents(Select).find(select => select.props('ariaLabel') === '用于 CC Switch 的 API Key')!.vm.$emit('update:modelValue', 89)
    await flushPromises()
    const fourth = wrapper.find('[aria-label="第四步：CC Switch"]')
    expect(fourth.text()).toContain('我的第二个 Codex')
    expect(new URL(fourth.find('a[href^="ccswitch://"]').attributes('href')).searchParams.get('apiKey')).toBe('sk-second')
    wrapper.unmount()
  })

  it('finds an existing usable key beyond the first page', async () => {
    api.listKeys.mockResolvedValueOnce({ items: [{ ...existingKey, status: 'inactive' }], total: 2 }).mockResolvedValueOnce({ items: [{ ...existingKey, id: 89, name: '第二页的 Codex', key: 'sk-next-page' }], total: 2 })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    expect(api.listKeys).toHaveBeenNthCalledWith(1, 1, 100)
    expect(api.listKeys).toHaveBeenNthCalledWith(2, 2, 100)
    expect(wrapper.find('[aria-label="第三步：API Key"]').text()).toContain('已完成')
    expect(new URL(wrapper.find('a[href^="ccswitch://"]').attributes('href')).searchParams.get('apiKey')).toBe('sk-next-page')
    wrapper.unmount()
  })

  it('does not import a disabled, expired or exhausted key', async () => {
    api.listKeys.mockResolvedValue({ items: [{ ...existingKey, status: 'inactive' }, { ...existingKey, id: 89, expires_at: '2020-01-01T00:00:00Z' }, { ...existingKey, id: 90, quota: 10, quota_used: 10 }], total: 3 })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    expect(wrapper.find('[aria-label="第三步：API Key"]').text()).toContain('未完成')
    expect(wrapper.find('a[href^="ccswitch://"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('creates a key when needed, then reuses it for import', async () => {
    api.createKey.mockResolvedValue(existingKey)
    api.listKeys.mockResolvedValueOnce({ items: [], total: 0 }).mockResolvedValue({ items: [existingKey], total: 1 })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '创建 API Key')!.trigger('click')
    await flushPromises()
    expect(api.createKey).toHaveBeenCalledWith('我的 Codex', 9)
    expect(wrapper.find('[aria-label="第三步：API Key"]').text()).toContain('已完成')
    expect(new URL(wrapper.find('a[href^="ccswitch://"]').attributes('href')).searchParams.get('apiKey')).toBe(existingKey.key)
    wrapper.unmount()
  })

  it('shows an explicit retry when existing keys cannot be loaded', async () => {
    api.listKeys.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('加载已有 API Key 失败')
    expect(wrapper.find('a[href^="ccswitch://"]').exists()).toBe(false)
    expect(api.createKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('configures five account-count tiers and confirms subscription names', async () => {
    const wrapper = mount(AdminOnboardingView, { global })
    await flushPromises()
    const tierSelectors = wrapper.findAllComponents(Select).filter(select => select.props('ariaLabel')?.endsWith('对应订阅'))
    expect(tierSelectors).toHaveLength(5)
    tierSelectors[1]!.vm.$emit('update:modelValue', 9)
    await wrapper.findAll('button').find(button => button.text() === '保存配置')!.trigger('click')
    expect(api.updateConfig).not.toHaveBeenCalled()
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('2 个 Plus 等值：Plus 周额度')
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ subscription_rules: expect.objectContaining({ '2': { group_id: 9, validity_days: 30 } }) }))
    expect(wrapper.text()).not.toContain('消耗单位')
    wrapper.unmount()
  })

  it('labels a team account as Business Standard', async () => {
    api.userOverview.mockResolvedValueOnce({ accounts: [{ ...account, plan_type: 'team' }], account_units: 1, subscriptions: [], ready_for_key: true, ready_for_account: true, config })
    const wrapper = mount(UserOnboardingView, { global })
    await flushPromises()
    expect(wrapper.find('table').text()).toContain('Business Standard')
    expect(wrapper.find('table').text()).not.toContain('Business Premium')
    wrapper.unmount()
  })
  it('shows the owner account and confirms pause before updating it', async () => {
    const wrapper = mount(UserOnboardingView, { global, attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('gpt@example.com')
    const pause = wrapper.findAll('button').find(button => button.text() === '暂停')!
    await pause.trigger('click')
    expect(api.pause).not.toHaveBeenCalled()
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('共享 GPT Plus')
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.pause).toHaveBeenCalledWith(42, true)
    wrapper.unmount()
  })

  it('keeps a failed account operation visible to the user', async () => {
    api.remove.mockRejectedValueOnce({ response: { data: { message: '账号不存在' } } })
    const wrapper = mount(UserOnboardingView, { global, attachTo: document.body })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '删除')!.trigger('click')
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.showError).toHaveBeenCalledWith('账号不存在')
    expect(wrapper.text()).toContain('共享 GPT Plus')
    wrapper.unmount()
  })

  it('identifies both the user and GPT account when assigning', async () => {
    const wrapper = mount(AdminOnboardingView, { global, attachTo: document.body })
    await flushPromises()
    expect(wrapper.text()).toContain('owner@example.com')
    await wrapper.findAll('button').find(button => button.text() === '分配账号')!.trigger('click')
    const selector = wrapper.findAllComponents(Select).find(select => select.attributes('aria-label') === '选择 GPT 账号' || select.props('ariaLabel') === '选择 GPT 账号')!
    selector.vm.$emit('update:modelValue', 42)
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '下一步')!.trigger('click')
    const message = wrapper.findComponent(ConfirmDialog).props('message')
    expect(message).toContain('owner@example.com')
    expect(message).toContain('共享 GPT Plus')
    expect(api.bindAccount).not.toHaveBeenCalled()
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.bindAccount).toHaveBeenCalledWith(42, 7)
    expect(wrapper.find('input[type="number"][aria-label="用户 ID"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('公共回退')
    wrapper.unmount()
  })

  it('binds a selected existing Discord member after showing both identities', async () => {
    const wrapper = mount(AdminOnboardingView, { global, attachTo: document.body })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '绑定 Discord')!.trigger('click')
    await flushPromises()
    const selector = wrapper.findAllComponents(Select).find(select => select.props('ariaLabel') === '选择 Discord 成员')!
    selector.vm.$emit('update:modelValue', '12345')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '下一步')!.trigger('click')
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('Discord 昵称')
    expect(wrapper.findComponent(ConfirmDialog).props('message')).toContain('owner@example.com')
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(api.bindDiscord).toHaveBeenCalledWith(7, '12345')
    wrapper.unmount()
  })
})
