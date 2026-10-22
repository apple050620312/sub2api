<template>
  <AppLayout>
    <div class="w-full space-y-8">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('serviceCenter.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('serviceCenter.description') }}</p>
        </div>
        <button class="btn btn-secondary inline-flex h-9 w-9 items-center justify-center" :disabled="loading" :title="t('dynamic5h.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </header>
      <div class="flex flex-wrap gap-3" role="tablist" :aria-label="t('serviceCenter.title')">
        <button role="tab" :aria-selected="tab === 'setup'" :class="tab === 'setup' ? 'btn btn-primary' : 'btn btn-secondary'" @click="tab = 'setup'">{{ t('serviceCenter.setupTitle') }}</button>
        <button role="tab" :aria-selected="tab === 'usage'" :class="tab === 'usage' ? 'btn btn-primary' : 'btn btn-secondary'" @click="tab = 'usage'">{{ t('dynamic5h.title') }}</button>
        <button role="tab" :aria-selected="tab === 'pool'" :class="tab === 'pool' ? 'btn btn-primary' : 'btn btn-secondary'" @click="tab = 'pool'">号池总览</button>
      </div>
      <div v-show="tab === 'usage'" class="space-y-8" role="tabpanel">
      <div v-if="loading && !status" class="flex justify-center py-16"><LoadingSpinner /></div>
      <div v-else-if="status" class="space-y-8">
        <div v-if="!status.enabled" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('dynamic5h.disabled') }}</div>
        <div v-else-if="!status.quota_available" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('dynamic5h.unavailable') }}</div>
        <div v-else class="space-y-8">
          <section class="rounded-lg border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-800">
            <div class="mb-8 flex flex-wrap items-center justify-between gap-4">
              <span :class="stateClass" class="rounded-full px-3 py-1 text-sm font-semibold">{{ stateLabel }}</span>
              <span v-if="status.currently_limited" class="rounded-full bg-red-100 px-3 py-1 text-sm font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-300">{{ t('dynamic5h.limited') }}</span>
              <span v-else class="rounded-full bg-emerald-100 px-3 py-1 text-sm font-semibold text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{{ t('dynamic5h.available') }}</span>
            </div>
            <UsageWindowBar :label="t('dynamic5h.usage')" :value="status.usage_percent" />
            <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ money(status.usage_usd) }} / {{ money(status.limit_usd) }} · {{ t('dynamic5h.weeklyAllowance', { value: money(status.seven_day_limit_usd) }) }}</p>
            <p class="mt-3 text-sm font-medium" :class="warningClass">{{ t(`dynamic5h.warning.${status.warning_level || 'normal'}`) }}</p>
            <div class="mt-8 grid gap-5 sm:grid-cols-2">
              <div class="rounded-lg bg-gray-50 p-5 dark:bg-dark-700/50">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('dynamic5h.remaining') }}</p>
                <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ money(status.remaining_usd) }} ({{ status.remaining_percent.toFixed(1) }}%)</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-5 dark:bg-dark-700/50">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('dynamic5h.recover') }}</p>
                <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{{ formatTime(status.recover_at) }}</p>
              </div>
            </div>
            <div class="mt-4 flex flex-wrap gap-4 text-xs text-gray-500 dark:text-gray-400">
              <span v-if="status.exempt">{{ t('dynamic5h.exempt') }}</span>
            </div>
          </section>
          <section class="rounded-lg border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-800">
            <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('dynamic5h.rulesTitle') }}</h2>
            <ul class="mt-3 list-disc space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-gray-300">
              <li>{{ t('dynamic5h.ruleNormal') }}</li>
              <li>{{ t('dynamic5h.rulePeak') }}</li>
              <li>{{ t('dynamic5h.ruleDynamic') }}</li>
              <li>{{ t('dynamic5h.ruleRecover') }}</li>
            </ul>
          </section>
        </div>
        <div v-if="status.enabled && status.pool" class="space-y-5">
          <section class="rounded-lg border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-800">
            <div class="mb-6 flex flex-wrap items-start justify-between gap-4">
              <div><h2 class="font-semibold text-gray-900 dark:text-white">压力值与容量</h2><p class="mt-2 text-sm leading-6 text-gray-500">压力决定是否进入尖峰，不改变你的已消费金额。容量以一支账号的完整窗口为 1 个账号等值。</p></div>
              <span class="rounded-full bg-gray-50 px-3 py-1 text-sm dark:bg-dark-700">{{ pressureState }}</span>
            </div>
            <div class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
              <div v-for="metric in pressureMetrics" :key="metric.label" class="rounded-lg bg-gray-50 p-5 dark:bg-dark-700/50">
                <p class="text-xs text-gray-500">{{ metric.label }}</p><p class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">{{ metric.value }}</p>
              </div>
            </div>
          </section>
          <div class="rounded-lg border border-gray-200 bg-white px-5 py-4 text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400">
            {{ t('dynamic5h.updatedAt') }}: {{ formatTime(status.pool.evaluated_at) }}
            <span v-if="status.pool.stale" class="ml-2 text-amber-600">{{ t('dynamic5h.stale') }}</span>
          </div>
        </div>
      </div>
      </div>
      <div v-show="tab === 'pool'" role="tabpanel" class="space-y-6">
        <section v-if="status?.pool" class="space-y-8 rounded-lg border border-gray-200 bg-white p-6 dark:border-dark-700 dark:bg-dark-800">
          <div><h2 class="font-semibold text-gray-900 dark:text-white">号池总览</h2><p class="mt-2 text-sm leading-6 text-gray-500">展示全部账号的名称与窗口状态，邮箱自动隐藏。5h 平均剩余按已启用、可调度、7d 额度未耗尽且有有效窗口数据的账号计算；临时限流、过载或暂时不可用不会改变平均值。</p></div>
          <div class="grid gap-5 sm:grid-cols-2">
            <div v-for="window in poolWindows" :key="window.label" class="rounded-lg bg-gray-50 p-5 dark:bg-dark-700/50">
              <p class="text-xs text-gray-500">{{ window.label }} 平均剩余容量</p>
              <p class="mt-3 text-2xl font-semibold">{{ formatPercent(window.used === undefined ? undefined : Math.max(0, 100 - window.used)) }}</p>
              <p class="mt-3 text-sm text-gray-500">平均用量 {{ formatPercent(window.used) }} · {{ window.samples }} 个样本</p>
            </div>
          </div>
          <div class="overflow-x-auto">
            <table class="min-w-full text-sm">
              <thead><tr class="border-b text-left text-xs text-gray-500 dark:border-dark-700"><th class="px-4 py-4">账号</th><th class="px-4 py-4">5h 窗口</th><th class="px-4 py-4">7d 窗口</th><th class="px-4 py-4">状态</th></tr></thead>
              <tbody>
                <tr v-for="(account, index) in status.pool.accounts" :key="index" class="border-b border-gray-100 dark:border-dark-700">
                  <td class="min-w-40 px-4 py-5"><p class="break-words font-medium">{{ account.name || '未命名账号' }}</p><p class="mt-2 text-xs text-gray-500">{{ platformLabel(account.platform) }}</p></td>
                  <td class="min-w-56 px-4 py-5">
                    <UsageWindowBar v-if="account.five_hour_used_percent !== undefined" label="5h 用量" :value="account.five_hour_used_percent" />
                    <p v-else class="text-gray-500">暂无窗口数据</p>
                    <p class="mt-3 whitespace-nowrap text-xs text-gray-500">{{ resetRemaining(account.five_hour_reset_at, account.five_hour_used_percent) }}</p>
                  </td>
                  <td class="min-w-56 px-4 py-5">
                    <UsageWindowBar v-if="account.seven_day_used_percent !== undefined" label="7d 用量" :value="account.seven_day_used_percent" />
                    <p v-else class="text-gray-500">暂无窗口数据</p>
                    <p class="mt-3 whitespace-nowrap text-xs text-gray-500">{{ resetRemaining(account.seven_day_reset_at, account.seven_day_used_percent) }}</p>
                  </td>
                  <td class="whitespace-nowrap px-4 py-5">{{ accountReason(account.reason) }}</td>
                </tr>
              </tbody>
            </table>
            <p v-if="!status.pool.accounts?.length" class="py-10 text-center text-sm text-gray-500">暂无账号。</p>
          </div>
          <p class="text-sm leading-6 text-gray-500">数据更新时间：{{ formatTime(status.pool.evaluated_at) }}<span v-if="status.pool.stale" class="ml-2 text-amber-600">{{ t('dynamic5h.stale') }}</span></p>
        </section>
        <p v-else class="py-12 text-center text-sm text-gray-500">{{ loading ? '正在加载号池信息…' : '号池信息暂不可用，请刷新重试。' }}</p>
      </div>
      <div v-show="tab === 'setup'" role="tabpanel"><ServiceSetupPanel /></div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import UsageWindowBar from '@/components/service/UsageWindowBar.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ServiceSetupPanel from '@/components/service/ServiceSetupPanel.vue'
import { getDynamic5hPressure } from '@/api/serviceCenter'
import { useAppStore } from '@/stores/app'
import type { Dynamic5hUserStatus } from '@/types'

const { t } = useI18n()
const app = useAppStore()
const route = useRoute()
const tab = ref<'setup' | 'usage' | 'pool'>(route.query.code || route.query.tab === 'setup' ? 'setup' : route.query.tab === 'pool' ? 'pool' : 'usage')
const money = (value = 0) => `$${value.toFixed(2)}`
const status = ref<Dynamic5hUserStatus | null>(null)
const loading = ref(false)
const formatPercent = (value?: number) => value === undefined ? '-' : `${value.toFixed(1)}%`
const poolWindows = computed(() => status.value?.pool ? [
  { label: '5h', used: status.value.pool.five_hour_used_percent, samples: status.value.pool.five_hour_sample_count },
  { label: '7d', used: status.value.pool.seven_day_used_percent, samples: status.value.pool.seven_day_sample_count },
] : [])
const pressureState = computed(() => {
  const pressure = status.value?.pool?.pressure_status
  if (!pressure?.data_available || pressure.stale) return '压力数据暂不可用'
  return pressure.state === 'peak' ? '尖峰' : '非尖峰'
})
const pressureMetrics = computed(() => {
  const pressure = status.value?.pool?.pressure_status
  if (!pressure) return []
  const available = pressure.data_available && !pressure.stale
  const capacity = (value: number) => available ? `${value.toFixed(2)} 个账号等值` : '-'
  return [
    { label: '平滑压力值', value: available ? formatPercent(pressure.pressure * 100) : '-' },
    { label: '即时压力值', value: available ? formatPercent(pressure.raw_pressure * 100) : '-' },
    { label: '进入 / 退出尖峰门槛', value: `${formatPercent(pressure.peak_threshold * 100)} / ${formatPercent(pressure.normal_threshold * 100)}` },
    { label: '剩余 5h 容量', value: capacity(pressure.remaining_capacity) },
    { label: '每小时消耗估计', value: capacity(pressure.burn_rate_per_hour) },
    { label: '未来五小时需求估计', value: capacity(pressure.projected_demand) },
    { label: '下一小时恢复容量', value: capacity(pressure.capacity_recovering_next_hour) },
    { label: '有效 / 排除账号', value: `${pressure.account_count} / ${pressure.excluded_account_count}` },
    { label: '最近窗口恢复时间', value: pressure.next_reset_at ? formatTime(pressure.next_reset_at) : '-' },
  ]
})
const platformLabel = (platform: string) => ({ openai: 'OpenAI', anthropic: 'Anthropic', gemini: 'Gemini', kimi: 'Kimi', zhipu: '智谱', minimax: 'MiniMax', opencodego: 'OpenCode Go' })[platform] || '其他平台'
const accountReason = (reason: string) => ({ included: '计入压力评估', disabled: '已停用', unschedulable: '未参与调度', credential_expired: '授权已到期', rate_limited: '上游限流中', overloaded: '上游过载中', temporarily_unavailable: '暂时不可用', seven_day_exhausted: '7d 额度已用尽', identity_mismatch: '窗口身份待确认', snapshot_missing: '窗口数据不完整', snapshot_expired: '窗口数据已过期', unsupported_platform: '此平台不支持窗口统计' })[reason] || '状态待确认'
const resetRemaining = (reset?: string, used?: number) => {
  if (!reset) return used === 0 ? '窗口已恢复，等待下次使用' : '恢复时间待更新'
  const minutes = Math.ceil((new Date(reset).getTime() - Date.now()) / 60000)
  if (minutes <= 0) return '已到恢复时间，等待更新'
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor(minutes % 1440 / 60)
  return `剩余 ${days ? `${days} 天 ` : ''}${hours} 小时 ${minutes % 60} 分钟`
}
let timer: ReturnType<typeof setInterval> | undefined

const stateLabel = computed(() => t(`dynamic5h.${status.value?.state === 'peak' ? 'peak' : 'normal'}`))
const stateClass = computed(() => status.value?.state === 'peak' ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300' : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300')
const warningClass = computed(() => ({ limited: 'text-red-600', borrowed: 'text-amber-600', warning: 'text-amber-500', normal: 'text-emerald-600' }[status.value?.warning_level || 'normal']))
const formatTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : t('dynamic5h.now')
const load = async () => { if (loading.value) return; loading.value = true; try { status.value = await getDynamic5hPressure() } catch { app.showError('加载服务状态失败') } finally { loading.value = false } }

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
