import { describe, expect, it } from 'vitest'
import { toTraditionalChinese } from '../traditionalChinese'

describe('traditional Chinese browser conversion', () => {
  it('converts message strings recursively with character mappings only', async () => {
    const source = {
      account: '用户设置',
      nested: { terms: '软件与鼠标' },
      list: ['后台', '数据'],
    }

    await expect(toTraditionalChinese(source)).resolves.toEqual({
      account: '用戶設置',
      nested: { terms: '軟件與鼠標' },
      list: ['後臺', '數據'],
    })
  })
})
