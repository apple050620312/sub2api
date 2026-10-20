<template>
  <div class="space-y-6 rounded-lg border border-gray-200 p-5 dark:border-dark-700">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h3 class="font-semibold">Gmail 重置监测</h3><p class="mt-2 text-sm text-gray-500">仅分析指定发件人的通知。首次连接或重新启用时从新邮件开始，历史通知不会补触发。</p></div>
      <button class="btn btn-secondary" :disabled="busy" @click="load">刷新监测信息</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-if="form && overview">
      <p class="rounded-lg bg-gray-50 p-4 text-sm leading-6 dark:bg-dark-900">{{ overview.connected ? `已连接：${overview.email}` : '尚未连接 Gmail' }}。Google OAuth 只申请 Gmail 只读权限；凭据、刷新令牌和模型密钥加密保存在数据库。断开后可在 Google 账号中撤销授权。</p>
      <div class="grid gap-5 md:grid-cols-2">
        <label class="space-y-2 text-sm"><span>导入 Google credentials JSON</span><input type="file" accept=".json,application/json" class="input" @change="importCredentials" /><span class="block text-xs text-gray-500">{{ credentialsJSON ? '已读取新凭据，保存后生效并需重新授权' : overview.credentials_configured ? '已配置，留空保留' : '请上传 Google Cloud 的 OAuth 客户端凭据' }}</span></label>
        <label class="space-y-2 text-sm"><span>OAuth 回调地址</span><input v-model="form.redirect_uri" class="input" /><span class="block text-xs text-gray-500">在 Google Cloud 的已授权重定向 URI 中配置完全相同的地址。</span></label>
        <label class="space-y-2 text-sm"><span>监测发件人（每行一个）</span><textarea v-model="senders" rows="3" class="input" /></label>
        <label class="space-y-2 text-sm"><span>附加 Gmail 查询条件</span><input v-model="form.query" class="input" placeholder="可留空，例如 subject:Codex" /><span class="block text-xs text-gray-500">发件人白名单、授权开始时间和最大邮件时效始终额外校验。</span></label>
        <label class="space-y-2 text-sm"><span>模型 API 基础地址（包含 /v1）</span><input v-model="form.model_base_url" class="input" placeholder="https://example.com/v1" /></label>
        <label class="space-y-2 text-sm"><span>模型名称</span><input v-model="form.model" class="input" /></label>
        <label class="space-y-2 text-sm"><span>模型 API Key</span><input v-model="modelKey" type="password" autocomplete="new-password" class="input" :placeholder="overview.model_key_configured ? '已配置，留空保留' : '请输入 API Key'" /></label>
        <label class="space-y-2 text-sm"><span>模型接口格式</span><select v-model="form.api_format" class="input"><option value="chat_completions">Chat Completions</option><option value="responses">Responses</option></select></label>
      </div>
      <p class="text-sm leading-6 text-gray-500">指定来源的邮件主题、发件人和正文会发送给上面配置的模型服务。不会发送其他邮件、附件或 Gmail 令牌。</p>
      <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <label v-for="field in numberFields" :key="field.key" class="space-y-2 text-sm"><span>{{ field.label }}</span><input v-model.number="form[field.key]" type="number" :min="field.min" :max="field.max" :step="field.step || 1" class="input" /></label>
      </div>
      <div class="flex flex-wrap gap-5 text-sm">
        <label class="flex items-center gap-2"><input v-model="form.direct_reset_enabled" type="checkbox" />处理已确认的直接重置</label>
        <label class="flex items-center gap-2"><input v-model="form.banked_reset_enabled" type="checkbox" />处理已确认的重置券发放</label>
        <label class="flex items-center gap-2"><input v-model="form.require_authenticated_sender" type="checkbox" />要求 Gmail 的 DMARC 验证通过</label>
      </div>
      <details><summary class="cursor-pointer text-sm font-medium">自定义模型分析规则</summary><textarea v-model="form.prompt" rows="8" class="input mt-3" /><p class="mt-2 text-xs text-gray-500">作为补充规则使用。结构化输出、原文证据校验和置信度门槛始终保留。概率预警与未来公告默认忽略。</p></details>
      <div class="rounded-lg bg-gray-50 p-4 text-sm leading-6 dark:bg-dark-900">默认每 5 分钟检查；首封确认通知后等待 15 分钟再恢复配额；2 小时内的重置通知合并；执行后冷却 6 小时。两家来源先后通知只重置一次。修改监测配置会取消尚未执行的待处理事件，已生效的冷却保留。分析失败默认最多尝试 3 次，仍失败则跳过并留下记录。</div>
      <div class="flex flex-wrap gap-3">
        <button class="btn btn-primary" :disabled="busy" @click="confirmation = 'save'">保存 Gmail 配置</button>
        <button class="btn btn-secondary" :disabled="busy || !overview.credentials_configured" @click="authorize">{{ overview.connected ? '重新授权 Gmail' : '连接 Gmail' }}</button>
        <button v-if="overview.connected" class="btn btn-secondary" :disabled="busy" @click="confirmation = 'disconnect'">断开 Gmail</button>
      </div>
      <p class="text-xs leading-5 text-gray-500">先保存 Gmail 配置并完成授权，再启用上方的全员配额重置并保存服务配置。重新授权会从授权后的新邮件开始。</p>
      <details v-if="overview.recent_mail.length"><summary class="cursor-pointer text-sm font-medium">最近邮件判定记录（最多 30 条）</summary><div class="mt-4 divide-y divide-gray-100 dark:divide-dark-700"><article v-for="(mail, index) in overview.recent_mail" :key="index" class="space-y-2 py-4 text-sm"><p class="font-medium">{{ mail.subject }}</p><p class="break-words text-xs text-gray-500">{{ mail.sender }} · {{ new Date(mail.received_at).toLocaleString('zh-CN', { hour12: false }) }}</p><p>{{ kindLabel(mail.kind) }} · {{ (mail.confidence * 100).toFixed(1) }}% · {{ outcomeLabel(mail.outcome) }}</p><p v-if="mail.evidence" class="break-words text-gray-500">原文依据：{{ mail.evidence }}</p></article></div></details>
    </template>
    <ConfirmDialog :show="!!confirmation" :title="confirmation === 'disconnect' ? '断开 Gmail' : '保存 Gmail 配置'" :message="confirmation === 'disconnect' ? '断开后停止读取邮件并移除本服务保存的刷新令牌。确定继续？' : '保存将更新邮件监测规则并取消尚未执行的待处理事件；如上传新凭据，需要重新授权。确定继续？'" @cancel="confirmation = null" @confirm="confirm" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { gmailResetAPI, type GmailResetConfig, type GmailResetOverview } from '@/api/admin/gmailReset'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const emit = defineEmits<{ updated: [] }>()
const overview = ref<GmailResetOverview | null>(null)
const form = ref<GmailResetConfig | null>(null)
const senders = ref('')
const credentialsJSON = ref('')
const modelKey = ref('')
const error = ref('')
const busy = ref(false)
const confirmation = ref<'save' | 'disconnect' | null>(null)
type NumericKey = { [Key in keyof GmailResetConfig]: GmailResetConfig[Key] extends number ? Key : never }[keyof GmailResetConfig]
const numberFields: Array<{ key: NumericKey; label: string; min: number; max: number; step?: number }> = [
  { key: 'poll_minutes', label: '检查间隔（分钟）', min: 1, max: 1440 },
  { key: 'propagation_minutes', label: '传播等待（分钟）', min: 0, max: 1440 },
  { key: 'aggregation_minutes', label: '事件合并窗口（分钟）', min: 1, max: 2880 },
  { key: 'cooldown_minutes', label: '执行后冷却（分钟）', min: 1, max: 10080 },
  { key: 'max_age_hours', label: '最大邮件时效（小时）', min: 1, max: 168 },
  { key: 'min_confidence', label: '最低置信度（0～1）', min: 0.5, max: 1, step: 0.01 },
  { key: 'batch_size', label: '每轮最多处理邮件数', min: 1, max: 100 },
  { key: 'max_model_attempts', label: '单封邮件最多分析次数', min: 1, max: 10 },
  { key: 'retention_days', label: '判定记录保留天数', min: 7, max: 365 },
  { key: 'model_timeout_seconds', label: '模型超时（秒）', min: 5, max: 120 },
  { key: 'max_output_tokens', label: '模型最大输出 token', min: 128, max: 8192 },
  { key: 'max_body_chars', label: '正文最多发送字符数', min: 1000, max: 100000 },
]
const kindLabel = (kind: string) => ({ direct_reset: '直接重置', banked_reset: '重置券发放', forecast: '预测或预警', unknown: '未确认' })[kind] || kind
const outcomeLabel = (outcome: string) => ({ pending: '等待传播', applied: '已执行重置', merged: '归并至已有事件', superseded: '已被较新的事件替代', cooldown: '冷却期内忽略', ignored: '未达到触发条件', kind_disabled: '此类事件未启用', untrusted_sender: '发件人验证未通过', stale: '历史或过期邮件', empty_body: '正文为空', analysis_retry: '分析失败，等待重试', analysis_failed: '分析失败，已达尝试上限', invalid_message: '邮件格式无效' })[outcome] || '未确认'
const message = (value: unknown) => (value as { response?: { data?: { message?: string } }; message?: string })?.response?.data?.message || 'Gmail 操作失败，请检查配置后重试'

async function load() {
  try {
    const data = await gmailResetAPI.get()
    form.value = structuredClone(data.config)
    overview.value = data
    if (!form.value.redirect_uri) form.value.redirect_uri = `${window.location.origin}/admin/service-center`
    senders.value = form.value.senders.join('\n')
    error.value = ''
    emit('updated')
  } catch (value) { error.value = message(value) }
}
async function importCredentials(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (file.size > 65536) { error.value = 'credentials JSON 不能超过 64 KB'; input.value = ''; return }
  try {
    const text = await file.text()
    JSON.parse(text)
    credentialsJSON.value = text
    error.value = ''
  } catch { error.value = 'credentials JSON 文件格式无效' }
}
async function confirm() {
  const action = confirmation.value
  if (!action || !form.value || busy.value) return
  confirmation.value = null
  busy.value = true
  try {
    if (action === 'disconnect') await gmailResetAPI.disconnect()
    else {
      await gmailResetAPI.save({ ...form.value, senders: senders.value.split(/[\n,]+/).map(value => value.trim().toLowerCase()).filter(Boolean) }, credentialsJSON.value, modelKey.value)
      credentialsJSON.value = ''
      modelKey.value = ''
    }
    await load()
    app.showSuccess('Gmail 配置已更新')
  } catch (value) { error.value = message(value) } finally { busy.value = false }
}
async function authorize() {
  if (busy.value) return
  busy.value = true
  try { window.location.assign((await gmailResetAPI.start()).auth_url) } catch (value) { error.value = message(value); busy.value = false }
}
onMounted(async () => {
  const callback = new URL(window.location.href)
  const state = callback.searchParams.get('state') || ''
  if (state.startsWith('gmail-reset.')) {
    const code = callback.searchParams.get('code') || ''
    const denied = callback.searchParams.has('error')
    for (const key of ['code', 'state', 'scope', 'authuser', 'prompt', 'error', 'error_description', 'iss']) callback.searchParams.delete(key)
    window.history.replaceState({}, '', callback.pathname + callback.search + callback.hash)
    try {
      if (denied || !code) throw new Error('authorization denied')
      await gmailResetAPI.complete(code, state)
      app.showSuccess('Gmail 授权成功')
    } catch (value) { app.showError(message(value)) }
  }
  await load()
})
</script>
