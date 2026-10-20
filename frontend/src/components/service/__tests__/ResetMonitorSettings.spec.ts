import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ResetMonitorSettings from '../ResetMonitorSettings.vue'

const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), keys: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/resetMonitor', () => ({ resetMonitorAPI: api }))
vi.mock('@/api/keys', () => ({ keysAPI: { list: api.keys } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.showSuccess }) }))

const overview = {
  config: {
    channel_id: '1561630415750561863', author_ids: [],
    poll_seconds: 300, propagation_minutes: 30, cooldown_minutes: 720, banked_reset_enabled: true,
    model_api_key_id: 7, model: 'pool-model', api_format: 'responses', prompt: '',
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
    expect(wrapper.findAll('input[type="number"]')).toHaveLength(3)
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(1)
    expect(wrapper.find('details').attributes('open')).toBeUndefined()
    await wrapper.find('input[type="number"]').setValue(600)
    await wrapper.find('input[type="number"]').trigger('keydown', { key: 'Enter' })
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '保存监测配置')!.trigger('click')
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.find('[data-confirm] button').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith({ ...overview.config, poll_seconds: 600 })
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
