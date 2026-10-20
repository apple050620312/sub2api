import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import GmailResetSettings from '../GmailResetSettings.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), disconnect: vi.fn(), complete: vi.fn(), start: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/gmailReset', () => ({ gmailResetAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.showError, showSuccess: api.showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const overview = {
  config: {
    senders: ['notification@willcodexreset.com', 'alerts@codex-resets.com'], query: '', redirect_uri: '',
    poll_minutes: 5, propagation_minutes: 15, aggregation_minutes: 120, cooldown_minutes: 360,
    max_age_hours: 24, batch_size: 20, max_model_attempts: 3, retention_days: 30,
    require_authenticated_sender: true, direct_reset_enabled: true, banked_reset_enabled: true,
    min_confidence: 0.95, model_base_url: '', model: '', api_format: 'responses', prompt: 'Classify resets.',
    model_timeout_seconds: 60, max_output_tokens: 1024, max_body_chars: 24000,
  },
  credentials_configured: true, model_key_configured: true, connected: true,
  email: 'monitor@example.com', monitor_since: '', recent_mail: [],
}
const global = { stubs: { ConfirmDialog: { props: ['show', 'title', 'message'], template: '<div v-if="show" data-confirm><p>{{ message }}</p><button @click="$emit(\'confirm\')">确认应用</button><button @click="$emit(\'cancel\')">取消</button></div>' } } }

beforeEach(() => {
  vi.clearAllMocks()
  window.history.replaceState({}, '', '/')
  api.get.mockResolvedValue(structuredClone(overview))
  api.save.mockResolvedValue(overview)
  api.disconnect.mockResolvedValue(undefined)
  api.complete.mockResolvedValue(undefined)
})

describe('Gmail reset settings', () => {
  it('applies settings only after the save button and confirmation', async () => {
    const wrapper = mount(GmailResetSettings, { global })
    await flushPromises()
    const firstNumber = wrapper.find('input[type="number"]')
    await firstNumber.setValue(10)
    await firstNumber.trigger('keydown', { key: 'Enter' })
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '保存 Gmail 配置')!.trigger('click')
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.find('[data-confirm] button').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ poll_minutes: 10, max_model_attempts: 3 }), '', '')
    wrapper.unmount()
  })

  it('requires confirmation before disconnecting the mailbox', async () => {
    const wrapper = mount(GmailResetSettings, { global })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '断开 Gmail')!.trigger('click')
    expect(api.disconnect).not.toHaveBeenCalled()
    await wrapper.find('[data-confirm] button').trigger('click')
    await flushPromises()
    expect(api.disconnect).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('clears sensitive callback parameters before exchanging the code', async () => {
    window.history.replaceState({}, '', '/admin/service-center?code=private-code&state=gmail-reset.once&scope=gmail.readonly&tab=reset')
    api.complete.mockImplementation(async () => {
      expect(window.location.search).toBe('?tab=reset')
    })
    const wrapper = mount(GmailResetSettings, { global })
    await flushPromises()
    expect(api.complete).toHaveBeenCalledWith('private-code', 'gmail-reset.once')
    expect(api.showSuccess).toHaveBeenCalledWith('Gmail 授权成功')
    expect(wrapper.text()).not.toContain('private-code')
    wrapper.unmount()
  })
})
