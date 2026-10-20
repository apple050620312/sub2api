import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ResetMonitorSettings from '../ResetMonitorSettings.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), keys: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/resetMonitor', () => ({ resetMonitorAPI: api }))
vi.mock('@/api/keys', () => ({ keysAPI: { list: api.keys } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.showSuccess }) }))

const overview = {
  config: {
    channel_id: '1561630415750561863', author_ids: [], bots_only: true, require_embeds: true,
    poll_seconds: 5, propagation_minutes: 15, aggregation_minutes: 120, cooldown_minutes: 360,
    max_age_hours: 24, batch_size: 20, max_model_attempts: 3, retention_days: 30,
    direct_reset_enabled: true, banked_reset_enabled: true, min_confidence: 0.95,
    model_api_key_id: 7, model: 'pool-model', api_format: 'responses', prompt: 'Classify resets.',
    model_timeout_seconds: 60, max_output_tokens: 2048, max_body_chars: 24000,
  }, monitor_since: '', recent_messages: [],
}
const global = { stubs: { ConfirmDialog: { props: ['show', 'message'], template: '<div v-if="show" data-confirm><p>{{ message }}</p><button @click="$emit(\'confirm\')">确认应用</button><button @click="$emit(\'cancel\')">取消</button></div>' } } }

beforeEach(() => {
  vi.clearAllMocks()
  api.get.mockResolvedValue(structuredClone(overview))
  api.save.mockResolvedValue(overview)
  api.keys.mockResolvedValue({ items: [{ id: 7, name: '重置监测', key: 'must-not-display', status: 'active', group: { name: '号池', platform: 'openai' } }] })
})

describe('Reset monitor settings', () => {
  it('saves only after clicking and confirming, without sending a raw API key', async () => {
    const wrapper = mount(ResetMonitorSettings, { global })
    await flushPromises()
    await wrapper.find('input[type="number"]').setValue(10)
    await wrapper.find('input[type="number"]').trigger('keydown', { key: 'Enter' })
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '保存监测配置')!.trigger('click')
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.find('[data-confirm] button').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ poll_seconds: 10, model_api_key_id: 7 }))
    expect(JSON.stringify(api.save.mock.calls)).not.toContain('must-not-display')
    expect(wrapper.text()).not.toContain('must-not-display')
    wrapper.unmount()
  })

  it('keeps unsaved settings when confirmation is cancelled', async () => {
    const wrapper = mount(ResetMonitorSettings, { global })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '保存监测配置')!.trigger('click')
    await wrapper.findAll('[data-confirm] button')[1].trigger('click')
    expect(api.save).not.toHaveBeenCalled()
    expect(wrapper.find('[data-confirm]').exists()).toBe(false)
    wrapper.unmount()
  })
})
