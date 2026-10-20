<template>
  <AppLayout>
    <div class="space-y-8">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('serviceCenter.adminTitle') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('serviceCenter.description') }}</p>
        </div>
        <button class="btn btn-secondary inline-flex h-9 w-9 items-center justify-center" :disabled="loading" :title="t('admin.pressure5hPage.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </header>
      <div class="flex flex-wrap gap-3" role="tablist" :aria-label="t('serviceCenter.adminTitle')">
        <button role="tab" :aria-selected="tab === 'usage'" :class="tab === 'usage' ? 'btn btn-primary' : 'btn btn-secondary'" @click="tab = 'usage'">{{ t('dynamic5h.title') }}</button>
        <button v-for="panel in setupTabs" :key="panel.value" role="tab" :aria-selected="tab === panel.value" :class="tab === panel.value ? 'btn btn-primary' : 'btn btn-secondary'" @click="tab = panel.value">{{ panel.label }}</button>
      </div>
      <div v-show="tab === 'usage'" class="space-y-8" role="tabpanel">
      <div v-if="loading && !overview" class="flex justify-center py-16"><LoadingSpinner /></div>
      <div v-else-if="overview" class="space-y-8">
        <div v-if="!overview.pool.enabled" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('admin.pressure5hPage.disabled') }}</div>
        <div v-else class="space-y-8">
          <section class="grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
            <div v-for="metric in metrics" :key="metric.label" class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ metric.label }}</p>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ metric.value }}</p>
            </div>
          </section>

          <section class="card">
            <div class="border-b border-gray-100 p-6 dark:border-dark-700"><h2 class="font-semibold">{{ t('admin.pressure5hPage.users') }}</h2><p class="mt-2 text-sm text-gray-500">五小时额度为有效订阅七天美元额度的 15%。非尖峰不额外限流。</p></div>
            <DataTable :columns="columns" :data="overview.users" row-key="user_id" :actions-count="2">
              <template #cell-email="{ row }"><p class="whitespace-nowrap font-medium">{{ row.email || '已删除用户' }}</p><span class="mt-1 inline-block text-xs text-gray-500">{{ t(`admin.pressure5hPage.${row.active ? 'active' : 'inactive'}`) }}</span></template>
              <template #cell-usage="{ row }"><div class="w-44"><UsageWindowBar :label="t('admin.pressure5hPage.usage')" :value="row.usage_percent" /><p class="mt-2 text-xs text-gray-500">{{ money(row.usage_usd) }} / {{ money(row.limit_usd) }}</p></div></template>
              <template #cell-remaining_percent="{ value }">{{ value.toFixed(1) }}%</template>
              <template #cell-recover_at="{ value }"><span class="whitespace-nowrap">{{ formatTime(value) }}</span></template>
              <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" :disabled="saving" :aria-pressed="row.exempt" @click="askAction(row, 'exempt')">{{ row.exempt ? '取消豁免' : '豁免限流' }}</button><button class="btn btn-secondary btn-sm" :disabled="saving" @click="askAction(row, 'reset')">重置用量</button></div></template>
            </DataTable>
          </section>


          <details class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <summary class="cursor-pointer list-none px-6 py-5 font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.audit') }}</summary>
            <div v-if="overview.audit_logs.length === 0" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.noAudit') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700"><div v-for="log in overview.audit_logs" :key="log.id" class="grid gap-3 px-6 py-4 text-sm md:grid-cols-[170px_1fr_2fr]"><span>{{ formatTime(log.created_at) }}</span><span>{{ log.actor_email || '系统' }}</span><span>{{ t(`admin.pressure5hPage.auditActions.${log.action}`) }} · {{ log.user_email || '已删除用户' }}<small class="mt-1 block text-gray-400">{{ auditChange(log) }}</small></span></div></div>
          </details>
        </div>
      </div>
      </div>
      <div v-show="tab !== 'usage'" role="tabpanel"><ServiceManagementPanel :panel="tab === 'usage' ? 'accounts' : tab" /></div>
      <ConfirmDialog :show="!!confirmation" :title="confirmation?.title || ''" :message="confirmation?.message || ''" @cancel="confirmation = null" @confirm="confirmAction" />
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
import DataTable from '@/components/common/DataTable.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ServiceManagementPanel from '@/components/service/ServiceManagementPanel.vue'
import { getDynamic5hPressureOverview, resetDynamic5hUser, setDynamic5hUserPolicy } from '@/api/admin/dashboard'
import type { Dynamic5hAdminOverview, Dynamic5hAdminUserStatus } from '@/types'

const { t } = useI18n()
type SetupTab = 'accounts' | 'discord' | 'onboarding' | 'reset'
const setupTabs: Array<{ value: SetupTab; label: string }> = [{ value: 'accounts', label: '用户与账号' }, { value: 'discord', label: 'Discord' }, { value: 'onboarding', label: '引导与订阅' }, { value: 'reset', label: '重置监测' }]
const route = useRoute()
const tab = ref<'usage' | SetupTab>(route.query.tab === 'reset' ? 'reset' : 'usage')
const money = (value = 0) => `$${value.toFixed(2)}`
const overview = ref<Dynamic5hAdminOverview | null>(null)
const loading = ref(false)
const app = useAppStore()
const columns = [{ key: 'email', label: '用户邮箱' }, { key: 'usage', label: '五小时用量' }, { key: 'remaining_percent', label: '剩余' }, { key: 'recover_at', label: '恢复时间' }, { key: 'actions', label: '操作' }]
const confirmation = ref<{ user: Dynamic5hAdminUserStatus; action: 'exempt' | 'reset'; title: string; message: string } | null>(null)
const saving = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const metrics = computed(() => overview.value ? [
  { label: t('admin.pressure5hPage.pressure'), value: `${(overview.value.pool.pressure * 100).toFixed(1)}% (${t(`admin.pressure5hPage.states.${overview.value.pool.state}`)})` },
  { label: t('admin.pressure5hPage.effectiveAccounts'), value: overview.value.pool.account_count },
  { label: t('admin.pressure5hPage.activeUsers'), value: overview.value.pool.active_user_count },
  { label: t('admin.pressure5hPage.enforcement'), value: t(`admin.pressure5hPage.${overview.value.pool.peak_active && overview.value.pool.data_available && !overview.value.pool.stale ? 'on' : 'off'}`) },
  { label: t('admin.pressure5hPage.recoveringNextHour'), value: overview.value.pool.capacity_recovering_next_hour.toFixed(2) },
  { label: t('admin.pressure5hPage.excludedAccounts'), value: overview.value.pool.excluded_account_count },
  { label: t('admin.pressure5hPage.nextReset'), value: formatTime(overview.value.pool.next_reset_at) },
  { label: t('admin.pressure5hPage.thresholds'), value: `${(overview.value.pool.peak_threshold * 100).toFixed(0)}% / ${(overview.value.pool.normal_threshold * 100).toFixed(0)}%` },
] : [])
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const auditChange = (log: Dynamic5hAdminOverview['audit_logs'][number]) => log.action === 'usage_reset'
  ? `${(log.before_usage || 0).toFixed(2)} -> ${(log.after_usage || 0).toFixed(2)}`
  : `${log.old_policy?.exempt ? '豁免限流' : '按订阅限流'} → ${log.new_policy?.exempt ? '豁免限流' : '按订阅限流'}`
const load = async () => {
  if (loading.value) return
  loading.value = true
  try {
    overview.value = await getDynamic5hPressureOverview()
  } catch { app.showError('加载服务状态失败') } finally { loading.value = false }
}
function askAction(user: Dynamic5hAdminUserStatus, action: 'exempt' | 'reset') {
  const title = action === 'reset' ? '重置五小时用量' : user.exempt ? '取消限流豁免' : '豁免五小时限流'
  confirmation.value = { user, action, title, message: `确定对 ${user.email || '已删除用户'} 执行“${title}”？${action === 'reset' ? '仅清零额外五小时窗口，订阅原始账单和用量保持不变。' : '非尖峰始终不受此额外限制。'}` }
}
async function confirmAction() {
  const pending = confirmation.value
  if (!pending || saving.value) return
  confirmation.value = null
  saving.value = true
  try {
    if (pending.action === 'reset') await resetDynamic5hUser(pending.user.user_id)
    else await setDynamic5hUserPolicy(pending.user.user_id, { exempt: !pending.user.exempt })
    await load()
    app.showSuccess('已更新用户限流策略')
  } catch { app.showError('更新失败，请稍后重试') } finally { saving.value = false }
}

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
