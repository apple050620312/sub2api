<template>
  <div class="space-y-6 rounded-lg border border-gray-200 p-5 dark:border-dark-700">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h3 class="font-semibold">Discord 重置监测</h3><p class="mt-2 text-sm leading-6 text-gray-500">读取指定频道的新消息和 embed 更新，使用站内号池模型判断已经发生的重置。启用前的历史通知不会补触发。</p></div>
      <button class="btn btn-secondary" :disabled="busy" @click="load">刷新监测信息</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-if="form && overview">
      <div class="grid gap-5 md:grid-cols-2">
        <label class="space-y-2 text-sm"><span>监测频道 ID</span><input v-model="form.channel_id" class="input" inputmode="numeric" /><span class="block text-xs leading-5 text-gray-500">默认 1561630415750561863。机器人需在配置的服务器中，拥有查看频道、读取历史消息权限，并在 Discord Developer Portal 启用 Message Content Intent。</span></label>
        <label class="space-y-2 text-sm"><span>允许的消息作者或 Webhook ID（每行一个，可留空）</span><textarea v-model="authors" rows="3" class="input" /><span class="block text-xs text-gray-500">留空时按下方的机器人来源规则过滤；填写后仅处理这些来源。</span></label>
        <label class="space-y-2 text-sm"><span>用于调用号池的站内 API Key</span><select v-model.number="form.model_api_key_id" class="input"><option :value="0">请选择当前管理员的站内 Key</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }} · {{ key.group?.name || '未分组' }}</option><option v-if="form.model_api_key_id && !keys.some(key => key.id === form!.model_api_key_id)" :value="form.model_api_key_id">已配置的 Key（不在当前列表中）</option></select></label>
        <label class="space-y-2 text-sm"><span>号池模型名称</span><input v-model="form.model" class="input" /></label>
        <label class="space-y-2 text-sm"><span>站内模型接口</span><select v-model="form.api_format" class="input"><option value="responses">Responses</option><option value="chat_completions">Chat Completions</option></select></label>
      </div>
      <p class="rounded-lg bg-gray-50 p-4 text-sm leading-6 dark:bg-dark-900">模型请求始终通过本站网关，使用所选 Key 的分组、调度、模型映射及计费限制，无需外部模型地址或密钥。分析会消耗该 Key 的额度；Key 不可用或模型分析失败时不会重置配额。只分析消息文字及 embed 的标题、描述和字段，不访问链接或下载附件。</p>
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <label v-for="field in numberFields" :key="field.key" class="space-y-2 text-sm"><span>{{ field.label }}</span><input v-model.number="form[field.key]" type="number" :min="field.min" :max="field.max" :step="field.step || 1" class="input" /></label>
      </div>
      <div class="flex flex-wrap gap-5 text-sm">
        <label class="flex items-center gap-2"><input v-model="form.bots_only" type="checkbox" />仅处理机器人或 Webhook 消息</label>
        <label class="flex items-center gap-2"><input v-model="form.require_embeds" type="checkbox" />只处理带 embed 的消息</label>
        <label class="flex items-center gap-2"><input v-model="form.direct_reset_enabled" type="checkbox" />处理已确认的直接重置</label>
        <label class="flex items-center gap-2"><input v-model="form.banked_reset_enabled" type="checkbox" />处理已确认的重置券发放</label>
      </div>
      <details><summary class="cursor-pointer text-sm font-medium">自定义模型分析规则</summary><textarea v-model="form.prompt" rows="8" class="input mt-3" /><p class="mt-2 text-xs leading-5 text-gray-500">补充规则不会移除结构化输出、原文引用和置信度校验；概率预警和未来公告默认忽略。</p></details>
      <div class="rounded-lg bg-gray-50 p-4 text-sm leading-6 dark:bg-dark-900">默认每 5 秒检查；新内容仅分析一次，embed 后续变化会重新判断。首条确认通知后等待 15 分钟；2 小时内通知合并为同一事件；执行后冷却 6 小时。分析失败最多尝试 3 次。配置变更会取消尚未执行的事件，保留已生效冷却。</div>
      <button class="btn btn-primary" :disabled="busy" @click="confirmation = true">保存监测配置</button>
      <p class="text-xs leading-5 text-gray-500">先保存监测配置，再启用上方的全员配额重置并保存服务配置。更换频道或重新启用后从新消息开始。</p>
      <details v-if="overview.recent_messages.length"><summary class="cursor-pointer text-sm font-medium">最近消息判定记录（最多 30 条）</summary><div class="mt-4 divide-y divide-gray-100 dark:divide-dark-700"><article v-for="(record, index) in overview.recent_messages" :key="index" class="space-y-2 py-4 text-sm"><p class="font-medium">{{ record.subject }}</p><p class="break-words text-xs text-gray-500">作者 {{ record.sender }} · {{ new Date(record.received_at).toLocaleString('zh-CN', { hour12: false }) }}</p><p>{{ kindLabel(record.kind) }} · {{ (record.confidence * 100).toFixed(1) }}% · {{ outcomeLabel(record.outcome) }}</p><p v-if="record.evidence" class="break-words text-gray-500">原文依据：{{ record.evidence }}</p></article></div></details>
    </template>
    <ConfirmDialog :show="confirmation" title="保存重置监测配置" message="保存将更新消息来源与分析规则，并取消尚未执行的事件。模型分析会使用所选站内 Key 的额度。确定继续？" @cancel="confirmation = false" @confirm="confirm" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { resetMonitorAPI, type ResetMonitorConfig, type ResetMonitorOverview } from '@/api/admin/resetMonitor'
import { keysAPI } from '@/api/keys'
import type { ApiKey } from '@/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const emit = defineEmits<{ updated: [] }>()
const overview = ref<ResetMonitorOverview | null>(null)
const form = ref<ResetMonitorConfig | null>(null)
const keys = ref<ApiKey[]>([])
const authors = ref('')
const error = ref('')
const busy = ref(false)
const confirmation = ref(false)
type NumericKey = Exclude<{ [Key in keyof ResetMonitorConfig]: ResetMonitorConfig[Key] extends number ? Key : never }[keyof ResetMonitorConfig], 'model_api_key_id'>
const numberFields: Array<{ key: NumericKey; label: string; min: number; max: number; step?: number }> = [
  { key: 'poll_seconds', label: '检查间隔（秒）', min: 5, max: 3600 },
  { key: 'propagation_minutes', label: '传播等待（分钟）', min: 0, max: 1440 },
  { key: 'aggregation_minutes', label: '事件合并窗口（分钟）', min: 1, max: 2880 },
  { key: 'cooldown_minutes', label: '执行后冷却（分钟）', min: 1, max: 10080 },
  { key: 'max_age_hours', label: '最大消息时效（小时）', min: 1, max: 168 },
  { key: 'min_confidence', label: '最低置信度（0～1）', min: 0.5, max: 1, step: 0.01 },
  { key: 'batch_size', label: '每轮最多分析消息数', min: 1, max: 100 },
  { key: 'max_model_attempts', label: '同一内容最多分析次数', min: 1, max: 10 },
  { key: 'retention_days', label: '判定记录保留天数', min: 7, max: 365 },
  { key: 'model_timeout_seconds', label: '模型超时（秒）', min: 5, max: 120 },
  { key: 'max_output_tokens', label: '模型最大输出 token', min: 128, max: 8192 },
  { key: 'max_body_chars', label: '消息最多发送字符数', min: 1000, max: 100000 },
]
const kindLabel = (kind: string) => ({ direct_reset: '直接重置', banked_reset: '重置券发放', forecast: '预测或预警', unknown: '未确认' })[kind] || kind
const outcomeLabel = (outcome: string) => ({ pending: '等待传播', applied: '已执行重置', merged: '归并至已有事件', superseded: '已被较新的事件替代', cooldown: '冷却期内忽略', ignored: '未达到触发条件', kind_disabled: '此类事件未启用', stale: '历史或过期消息', analysis_retry: '分析失败，等待重试', analysis_failed: '分析失败，已达尝试上限' })[outcome] || '未确认'
const message = (value: unknown) => (value as { response?: { data?: { message?: string } } })?.response?.data?.message || '重置监测操作失败，请检查配置后重试'

async function load() {
  try {
    const [data, available] = await Promise.all([resetMonitorAPI.get(), keysAPI.list(1, 100)])
    form.value = structuredClone(data.config)
    overview.value = data
    keys.value = available.items.filter(key => key.status === 'active' && key.group?.platform === 'openai')
    authors.value = data.config.author_ids.join('\n')
    error.value = ''
    emit('updated')
  } catch (value) { error.value = message(value) }
}
async function confirm() {
  if (!confirmation.value || !form.value || busy.value) return
  confirmation.value = false
  busy.value = true
  try {
    await resetMonitorAPI.save({ ...form.value, author_ids: authors.value.split(/[\n,]+/).map(value => value.trim()).filter(Boolean) })
    await load()
    app.showSuccess('重置监测配置已更新')
  } catch (value) { error.value = message(value) } finally { busy.value = false }
}
onMounted(load)
</script>
