import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import UsageWindowBar from '../UsageWindowBar.vue'

describe('UsageWindowBar', () => {
  it('shows borrowed usage above 100% while capping the visual bar', () => {
    const wrapper = mount(UsageWindowBar, {
      props: { label: '5h Usage', value: 180 },
    })

    expect(wrapper.text()).toContain('180.0%')
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('100')
    expect(wrapper.find('[role="progressbar"] > div').attributes('style')).toContain('width: 100%')
    expect(wrapper.find('[role="progressbar"] > div').classes()).toContain('bg-red-500')
  })

  it('does not cap the numeric percentage', () => {
    const wrapper = mount(UsageWindowBar, {
      props: { label: '5h Usage', value: 1234.5 },
    })

    expect(wrapper.text()).toContain('1234.5%')
  })

  it('uses warning and available colors at the quota thresholds', async () => {
    const wrapper = mount(UsageWindowBar, {
      props: { label: '5h Usage', value: 75 },
    })

    expect(wrapper.find('[role="progressbar"] > div').classes()).toContain('bg-amber-500')

    await wrapper.setProps({ value: 74.9 })
    expect(wrapper.find('[role="progressbar"] > div').classes()).toContain('bg-emerald-500')
  })

  it('normalizes invalid negative usage to zero', () => {
    const wrapper = mount(UsageWindowBar, {
      props: { label: '5h Usage', value: -10 },
    })

    expect(wrapper.text()).toContain('0.0%')
    expect(wrapper.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('0')
  })
})
