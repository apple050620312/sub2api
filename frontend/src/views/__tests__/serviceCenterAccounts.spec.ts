import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const api = vi.hoisted(() => ({ userOverview: vi.fn(), adminOverview: vi.fn(), pause: vi.fn(), remove: vi.fn(), bindAccount: vi.fn(), bindDiscord: vi.fn(), members: vi.fn(), showError: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.showError, showSuccess: vi.fn() }) }))
vi.mock('@/api/user', () => ({ getOnboardingOverview: api.userOverview, setOnboardingAccountPaused: api.pause, deleteOnboardingAccount: api.remove }))
vi.mock('@/api/admin/onboarding', () => ({ onboardingAPI: { getOverview: api.adminOverview, bindAccount: api.bindAccount, bindDiscordMember: api.bindDiscord, getDiscordMembers: api.members } }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn().mockResolvedValue([{ id: 9, name: 'Plus 周额度', platform: 'openai', status: 'active', subscription_type: 'subscription' }]) }))
vi.mock('@/api/groups', () => ({ userGroupsAPI: { getAvailable: vi.fn().mockResolvedValue([]) } }))
vi.mock('@/api/keys', () => ({ keysAPI: { create: vi.fn() } }))

import UserOnboardingView from '@/views/user/OnboardingView.vue'
import AdminOnboardingView from '@/views/admin/OnboardingView.vue'

const account = { id: 42, name: '共享 GPT Plus', email: 'gpt@example.com', plan_type: 'plus', status: 'active', paused: false, schedulable: true, concurrency: 3, groups: ['Plus 周额度'], usage: { five_hour_used_percent: 25 }, created_at: '2026-10-18T00:00:00Z', updated_at: '2026-10-18T00:00:00Z' }
const config = { enabled: true, discord: { client_id: '', client_secret_configured: true, bot_token_configured: true, guild_id: 'guild', role_id: 'role', redirect_uri: '' }, plan_units: { plus: 1, pro: 5, business_premium: 5, business_standard: 1 }, subscription_rules: {}, openai_redirect_uri: '', ccswitch_download_url: '' }
const global = { stubs: { ServiceAccountDetails: true, BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, ConfirmDialog: { props: ['show', 'title', 'message'], template: '<div v-if="show">{{ title }} {{ message }}</div>' } } }

beforeEach(() => {
  vi.clearAllMocks()
  api.userOverview.mockResolvedValue({ accounts: [account], account_units: 1, subscriptions: [], ready_for_key: true, ready_for_account: true, config })
  api.adminOverview.mockResolvedValue({ config, users: [{ user_id: 7, email: 'owner@example.com', username: 'Owner', accounts: [], account_units: 0 }], unassigned_accounts: [account] })
  api.members.mockResolvedValue([{ id: '12345', username: 'discord-user', display_name: 'Discord 昵称' }])
})

describe('service center account management', () => {
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
