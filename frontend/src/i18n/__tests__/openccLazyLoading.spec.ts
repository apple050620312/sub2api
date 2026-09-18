import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

function readFrontendFile(path: string): string {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
}

describe('OpenCC lazy-loading contract', () => {
  it('keeps OpenCC out of the eagerly loaded shared vendor chunk', () => {
    for (const configPath of ['vite.config.ts', 'vite.config.js']) {
      const config = readFrontendFile(configPath)
      const openccRule = config.indexOf("id.includes('/opencc-js/')")
      const miscFallback = config.indexOf("return 'vendor-misc'")

      expect(openccRule).toBeGreaterThan(-1)
      expect(config.slice(openccRule, miscFallback)).toContain("return 'vendor-opencc'")
      expect(openccRule).toBeLessThan(miscFallback)
    }
  })
})
