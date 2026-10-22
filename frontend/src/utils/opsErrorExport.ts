import { getErrorLogDetail, listErrorLogs } from '@/api/admin/ops'
import type { OpsErrorDetail, OpsErrorListQueryParams } from '@/api/admin/ops'
import type { OpsErrorLogsResponse, OpsRequestOptions } from '@/api/admin/ops'
import { numericRequestTypeKind, requestTypeLabelKey } from '@/utils/errorBadges'

export type OpsErrorExportSource = {
  list: (params: OpsErrorListQueryParams, options?: OpsRequestOptions) => Promise<OpsErrorLogsResponse>
  getDetail: (id: number, options?: OpsRequestOptions) => Promise<OpsErrorDetail>
  sheetName?: string
}

type ExportOptions = {
  signal: AbortSignal
  t: (key: string) => string
  onProgress: (current: number, total: number) => void
}

const legacyErrorExportSource: OpsErrorExportSource = {
  list: listErrorLogs,
  getDetail: getErrorLogDetail,
}

export async function createErrorLogWorkbook(
  params: OpsErrorListQueryParams,
  options: ExportOptions,
  source: OpsErrorExportSource = legacyErrorExportSource
) {
  const query = { ...params, page_size: 100 }
  const { signal, t, onProgress } = options
  const XLSX = await import('xlsx')
  signal.throwIfAborted()
  const columns: Array<[keyof OpsErrorDetail, string]> = [
    ['id', 'ID'],
    ['created_at', t('usage.time')],
    ['user_email', t('admin.usage.user')],
    ['user_id', `${t('admin.usage.user')} ID`],
    ['api_key_name', t('usage.apiKeyFilter')],
    ['api_key_id', `${t('usage.apiKeyFilter')} ID`],
    ['api_key_deleted', t('admin.ops.errorLog.keyDeletedBadge')],
    ['account_name', t('admin.usage.account')],
    ['account_id', `${t('admin.usage.account')} ID`],
    ['group_name', t('admin.usage.group')],
    ['group_id', `${t('admin.usage.group')} ID`],
    ['platform', t('admin.ops.errorLog.platform')],
    ['model', t('usage.model')],
    ['requested_model', t('usage.requestedModel')],
    ['upstream_model', t('usage.sentUpstreamModel')],
    ['request_type', t('admin.ops.errorDetail.requestType')],
    ['phase', t('admin.ops.errorLog.phase')],
    ['type', t('admin.ops.errorLog.type')],
    ['status_code', t('admin.ops.errorLog.status')],
    ['error_owner', t('admin.ops.errorDetail.classificationKeys.owner')],
    ['error_source', t('admin.ops.errorDetail.classificationKeys.source')],
    ['severity', t('admin.ops.errorExport.severity')],
    ['message', t('admin.ops.errorDetail.message')],
    ['request_id', t('admin.usage.requestId')],
    ['client_request_id', t('admin.ops.errorExport.clientRequestId')],
    ['request_path', t('admin.ops.errorDetail.requestPath')],
    ['inbound_endpoint', t('usage.inboundEndpoint')],
    ['upstream_endpoint', t('usage.upstreamEndpoint')],
    ['user_agent', t('usage.userAgent')],
    ['client_ip', t('admin.usage.ipAddress')],
    ['resolved', t('admin.ops.errorDetails.resolved')],
    ['resolved_at', t('admin.ops.errorDetail.classificationKeys.resolvedAt')],
    ['resolved_by_user_id', `${t('admin.ops.errorDetail.classificationKeys.resolvedBy')} ID`],
    ['is_business_limited', t('admin.ops.errorDetail.businessLimited')],
    ['upstream_status_code', t('admin.ops.errorDetail.upstreamStatus')],
    ['upstream_error_message', t('admin.ops.errorDetail.payloads.upstream_message')],
    ['upstream_error_detail', t('admin.ops.errorDetail.payloads.upstream_detail')],
    ['upstream_errors', t('admin.ops.errorDetail.upstreamKeys.upstreamErrors')],
    ['error_body', t('admin.ops.errorDetail.errorBody')],
    ['auth_latency_ms', `${t('admin.ops.errorDetail.auth')} (ms)`],
    ['routing_latency_ms', `${t('admin.ops.errorDetail.routing')} (ms)`],
    ['upstream_latency_ms', `${t('admin.ops.errorDetail.upstream')} (ms)`],
    ['response_latency_ms', `${t('admin.ops.errorDetail.response')} (ms)`],
    ['time_to_first_token_ms', `${t('usage.firstToken')} (ms)`],
  ]
  const headers = columns.map(([, label]) => label)
  const continuationColumns = new Map<string, number>()
  const sheet = XLSX.utils.aoa_to_sheet([headers])
  let current = 0
  let page = 1
  while (true) {
    signal.throwIfAborted()
    const result = await source.list({ ...query, page }, { signal })
    signal.throwIfAborted()
    onProgress(current, result.total)
    for (let offset = 0; offset < result.items.length; offset += 5) {
      signal.throwIfAborted()
      const details = await Promise.all(result.items.slice(offset, offset + 5).map(async (item) => ({
        ...item,
        ...await source.getDetail(item.id, { signal }),
      })))
      signal.throwIfAborted()
      const rows = details.map((detail) => {
        const row: Array<string | number> = []
        columns.forEach(([field, label], index) => {
          let value = detail[field] ?? ''
          if (field === 'request_type') {
            const kind = numericRequestTypeKind(detail.request_type, detail.stream)
            value = kind ? t(requestTypeLabelKey(kind)) : ''
          } else if (typeof value === 'boolean') {
            value = t(value ? 'common.yes' : 'common.no')
          }
          if (typeof value !== 'string') {
            row[index] = value
            return
          }
          let part = 1
          let column = index
          do {
            let length = Math.min(value.length, 32767)
            if (length < value.length && /[\uD800-\uDBFF]/.test(value.charAt(length - 1))) length--
            row[column] = value.slice(0, length)
            value = value.slice(length)
            if (!value) break
            part++
            const key = `${field}:${part}`
            if (!continuationColumns.has(key)) {
              continuationColumns.set(key, headers.length)
              headers.push(`${label} (${part})`)
            }
            column = continuationColumns.get(key)!
          } while (value)
        })
        return row
      })
      XLSX.utils.sheet_add_aoa(sheet, rows, { origin: -1 })
      current += rows.length
      onProgress(current, result.total)
    }
    if (current >= result.total || result.items.length < query.page_size) break
    page++
  }
  signal.throwIfAborted()
  XLSX.utils.sheet_add_aoa(sheet, [headers], { origin: 'A1' })
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, sheet, source.sheetName || 'Errors')
  return workbook
}
