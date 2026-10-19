import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as XLSX from 'xlsx'
import { getErrorLogDetail, listErrorLogs } from '@/api/admin/ops'
import type { OpsErrorDetail, OpsErrorListQueryParams } from '@/api/admin/ops'
import { createErrorLogWorkbook } from '../opsErrorExport'

vi.mock('@/api/admin/ops', () => ({
  listErrorLogs: vi.fn(),
  getErrorLogDetail: vi.fn(),
}))

const makeError = (id: number, overrides: Partial<OpsErrorDetail> = {}): OpsErrorDetail => ({
  id,
  created_at: '2026-10-19T08:00:00Z',
  phase: 'upstream',
  type: 'upstream_error',
  error_owner: 'provider',
  error_source: 'upstream_http',
  severity: 'error',
  status_code: 502,
  platform: 'openai',
  model: 'gpt-5',
  resolved: false,
  client_request_id: `client-${id}`,
  request_id: `request-${id}`,
  message: 'Bad gateway',
  user_email: 'user@example.com',
  account_name: 'Pool account',
  group_name: 'Plus',
  error_body: '{"error":"Bad gateway"}',
  is_business_limited: false,
  upstream_status_code: 502,
  upstream_error_message: 'Provider unavailable',
  upstream_error_detail: 'Connection reset by peer',
  upstream_errors: '[{"account_id":7,"status_code":502,"message":"retry failed"}]',
  ...overrides,
})

const makeOptions = () => ({
  signal: new AbortController().signal,
  t: (key: string) => key,
  onProgress: vi.fn(),
})

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getErrorLogDetail).mockImplementation(async (id) => makeError(id))
})

describe('error log Excel export', () => {
  it('exports all filtered pages, retains full details and limits concurrent detail requests', async () => {
    const params: OpsErrorListQueryParams = { page: 4, page_size: 20, model: 'gpt-5', status_codes: '502', sort_by: 'status_code', sort_order: 'asc' }
    vi.mocked(listErrorLogs).mockImplementation(async ({ page }) => {
      params.model = 'changed-after-export-start'
      const items = page === 1 ? Array.from({ length: 100 }, (_, index) => makeError(index + 1)) : [makeError(101)]
      return { items, total: 101, page: page!, page_size: 100, pages: 2 }
    })
    let active = 0
    let peak = 0
    vi.mocked(getErrorLogDetail).mockImplementation(async (id) => {
      active++
      peak = Math.max(peak, active)
      await Promise.resolve()
      active--
      return makeError(id)
    })
    const options = makeOptions()
    const workbook = await createErrorLogWorkbook(params, options)
    const rows = XLSX.utils.sheet_to_json(workbook.Sheets.Errors)
    expect(rows).toHaveLength(101)
    expect(rows[100]).toMatchObject({
      ID: 101,
      'admin.usage.user': 'user@example.com',
      'admin.usage.account': 'Pool account',
      'admin.ops.errorLog.status': 502,
      'admin.ops.errorDetail.payloads.upstream_message': 'Provider unavailable',
      'admin.ops.errorDetail.payloads.upstream_detail': 'Connection reset by peer',
      'admin.ops.errorDetail.upstreamKeys.upstreamErrors': makeError(101).upstream_errors,
      'admin.ops.errorDetail.errorBody': makeError(101).error_body,
    })
    expect(peak).toBe(5)
    expect(listErrorLogs).toHaveBeenCalledTimes(2)
    for (const [index, [query, requestOptions]] of vi.mocked(listErrorLogs).mock.calls.entries()) {
      expect(query).toMatchObject({ page: index + 1, page_size: 100, model: 'gpt-5', status_codes: '502', sort_by: 'status_code', sort_order: 'asc' })
      expect(requestOptions?.signal).toBe(options.signal)
    }
    expect(getErrorLogDetail).toHaveBeenCalledWith(101, { signal: options.signal })
    expect(options.onProgress).toHaveBeenLastCalledWith(101, 101)
  })

  it('preserves oversized text and Unicode when the workbook is written and read', async () => {
    const text = 'x'.repeat(32766) + '😀' + '上游错误'.repeat(10000)
    vi.mocked(listErrorLogs).mockResolvedValue({ items: [makeError(1)], total: 1, page: 1, page_size: 100, pages: 1 })
    vi.mocked(getErrorLogDetail).mockResolvedValue(makeError(1, { message: text, upstream_errors: text }))
    const workbook = await createErrorLogWorkbook({}, makeOptions())
    const decoded = XLSX.read(XLSX.write(workbook, { type: 'array', bookType: 'xlsx' }), { type: 'array' })
    const [row] = XLSX.utils.sheet_to_json<Record<string, string>>(decoded.Sheets.Errors)
    for (const label of ['admin.ops.errorDetail.message', 'admin.ops.errorDetail.upstreamKeys.upstreamErrors']) {
      const parts = [row[label], row[`${label} (2)`], row[`${label} (3)`]]
      expect(parts.every((part) => part.length <= 32767)).toBe(true)
      expect(parts.join('')).toBe(text)
    }
  })

  it('stops without requesting details when cancelled during pagination', async () => {
    const controller = new AbortController()
    vi.mocked(listErrorLogs).mockImplementation(async () => {
      controller.abort()
      return { items: [makeError(1)], total: 1, page: 1, page_size: 100, pages: 1 }
    })
    await expect(createErrorLogWorkbook({}, { ...makeOptions(), signal: controller.signal })).rejects.toMatchObject({ name: 'AbortError' })
    expect(getErrorLogDetail).not.toHaveBeenCalled()
  })

  it('does not continue or return a partial workbook when a detail request fails', async () => {
    vi.mocked(listErrorLogs).mockResolvedValue({ items: Array.from({ length: 10 }, (_, index) => makeError(index + 1)), total: 10, page: 1, page_size: 100, pages: 1 })
    vi.mocked(getErrorLogDetail).mockRejectedValueOnce(new Error('Detail unavailable'))
    await expect(createErrorLogWorkbook({}, makeOptions())).rejects.toThrow('Detail unavailable')
    expect(getErrorLogDetail).toHaveBeenCalledTimes(5)
    expect(listErrorLogs).toHaveBeenCalledTimes(1)
  })

  it('exports an empty result as a workbook with headers', async () => {
    vi.mocked(listErrorLogs).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 100, pages: 0 })
    const workbook = await createErrorLogWorkbook({}, makeOptions())
    expect(XLSX.utils.sheet_to_json(workbook.Sheets.Errors, { header: 1 })).toHaveLength(1)
    expect(getErrorLogDetail).not.toHaveBeenCalled()
  })
})
