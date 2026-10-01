<template>
  <AppLayout>
    <div class="space-y-8">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.pressure5hPage.description') }}</p>
        </div>
        <button class="btn-secondary inline-flex h-9 w-9 items-center justify-center" :disabled="loading" :title="t('admin.pressure5hPage.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </header>

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
          <div v-if="overview.guaranteed_capacity_ratio > 1" class="rounded-lg border border-amber-300 bg-amber-50 px-5 py-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-200">
            {{ t('admin.pressure5hPage.overcommitted', { value: (overview.guaranteed_capacity_ratio * 100).toFixed(1) }) }}
          </div>

          <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <div class="border-b border-gray-200 px-6 py-5 dark:border-dark-700">
              <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.users') }}</h2>
            </div>
            <div v-if="!overview.pool.calibration_ready" class="p-7 text-sm text-gray-500">{{ t('admin.pressure5hPage.unavailable') }}</div>
            <div v-else-if="overview.users.length === 0" class="p-7 text-sm text-gray-500">{{ t('admin.pressure5hPage.noUsers') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div v-for="user in overview.users" :key="user.user_id" class="grid gap-6 px-6 py-9 lg:grid-cols-[minmax(250px,1.2fr)_minmax(180px,1.3fr)_100px_170px_minmax(250px,auto)] lg:items-center">
                <div class="min-w-0"><div class="flex flex-nowrap items-center gap-2"><p class="truncate font-medium text-gray-900 dark:text-white">{{ user.email || '用户' }}</p><span class="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium" :class="user.active ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'">{{ t(`admin.pressure5hPage.${user.active ? 'active' : 'inactive'}`) }}</span></div></div>
                <Dynamic5hUsageBar :label="t('admin.pressure5hPage.usage')" :value="user.usage_percent" />
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.remaining') }}</p><p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ user.remaining_percent.toFixed(1) }}%</p></div>
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.recover') }}</p><p class="text-sm text-gray-700 dark:text-gray-200">{{ formatTime(user.recover_at) }}</p></div>
                <div class="flex flex-wrap items-center gap-2">
                  <label class="flex h-8 items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.pressure5hPage.multiplier') }}
                    <input class="input h-8 w-20 px-2 text-xs" type="number" min="0.01" step="0.01" v-model="policyMultipliers[user.user_id]" @keydown.enter.prevent />
                  </label>
                  <button class="btn-primary h-8 px-2 text-xs" :disabled="saving || !validMultiplier(user)" @click="updateMultiplier(user)">{{ t('admin.pressure5hPage.apply') }}</button>
                  <button :disabled="saving" class="h-8 px-2 text-xs" :class="user.exempt ? 'btn-primary' : 'btn-secondary'" :aria-pressed="user.exempt" @click="toggleExempt(user)">{{ t('admin.pressure5hPage.exempt') }}</button>
                  <button class="btn-secondary h-8 px-2 text-xs" :disabled="saving" @click="resetUser(user.user_id)">{{ t('admin.pressure5hPage.resetUsage') }}</button>
                </div>
              </div>
            </div>
          </section>


          <details class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <summary class="cursor-pointer list-none px-6 py-5 font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.audit') }}</summary>
            <div v-if="overview.audit_logs.length === 0" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.noAudit') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700"><div v-for="log in overview.audit_logs" :key="log.id" class="grid gap-3 px-6 py-4 text-sm md:grid-cols-[170px_1fr_2fr]"><span>{{ formatTime(log.created_at) }}</span><span>ID {{ log.actor_user_id || '-' }}</span><span>{{ t(`admin.pressure5hPage.auditActions.${log.action}`) }} · 用户 {{ log.user_id }}<small class="mt-1 block text-gray-400">{{ auditChange(log) }}</small></span></div></div>
          </details>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Dynamic5hUsageBar from '@/components/common/Dynamic5hUsageBar.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getDynamic5hPressureOverview, resetDynamic5hUser, setDynamic5hUserPolicy } from '@/api/admin/dashboard'
import type { Dynamic5hAdminOverview, Dynamic5hAdminUserStatus } from '@/types'

const { t } = useI18n()
const overview = ref<Dynamic5hAdminOverview | null>(null)
const loading = ref(false)
const policyMultipliers = ref<Record<number, string | number>>({})
const saving = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const metrics = computed(() => overview.value ? [
  { label: t('admin.pressure5hPage.pressure'), value: `${(overview.value.pool.pressure * 100).toFixed(1)}% (${t(`admin.pressure5hPage.states.${overview.value.pool.state}`)})` },
  { label: t('admin.pressure5hPage.effectiveAccounts'), value: overview.value.pool.account_count },
  { label: t('admin.pressure5hPage.activeUsers'), value: overview.value.pool.active_user_count },
  { label: t('admin.pressure5hPage.enforcement'), value: t(`admin.pressure5hPage.${overview.value.pool.peak_active && overview.value.pool.calibration_ready ? 'on' : 'off'}`) },
  { label: t('admin.pressure5hPage.recoveringNextHour'), value: overview.value.pool.capacity_recovering_next_hour.toFixed(2) },
  { label: t('admin.pressure5hPage.excludedAccounts'), value: overview.value.pool.excluded_account_count },
  { label: t('admin.pressure5hPage.nextReset'), value: formatTime(overview.value.pool.next_reset_at) },
  { label: t('admin.pressure5hPage.thresholds'), value: `${(overview.value.pool.peak_threshold * 100).toFixed(0)}% / ${(overview.value.pool.normal_threshold * 100).toFixed(0)}%` },
] : [])
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const auditChange = (log: Dynamic5hAdminOverview['audit_logs'][number]) => log.action === 'usage_reset'
  ? `${(log.before_usage || 0).toFixed(2)} -> ${(log.after_usage || 0).toFixed(2)}`
  : `${log.old_policy?.exempt ? t('admin.pressure5hPage.exempt') : `${log.old_policy?.multiplier || 1}x`} -> ${log.new_policy?.exempt ? t('admin.pressure5hPage.exempt') : `${log.new_policy?.multiplier || 1}x`}`
const load = async () => {
  if (loading.value) return
  loading.value = true
  try {
    overview.value = await getDynamic5hPressureOverview()
    for (const user of overview.value.users) {
      policyMultipliers.value[user.user_id] ??= user.multiplier || 1
    }
  } finally { loading.value = false }
}
const validMultiplier = (user: Dynamic5hAdminUserStatus) => {
  const value = Number(policyMultipliers.value[user.user_id])
  return Number.isFinite(value) && value >= 0.01 && value !== (user.multiplier || 1)
}
const updateMultiplier = async (user: Dynamic5hAdminUserStatus) => {
  if (saving.value || !validMultiplier(user)) return
  const multiplier = Number(policyMultipliers.value[user.user_id])
  if (!window.confirm(t('admin.pressure5hPage.applyConfirm', { user: user.user_id, before: user.multiplier || 1, after: multiplier }))) return
  saving.value = true
  try {
    await setDynamic5hUserPolicy(user.user_id, { exempt: user.exempt, multiplier })
    delete policyMultipliers.value[user.user_id]
    await load()
  } finally { saving.value = false }
}
const toggleExempt = async (user: Dynamic5hAdminUserStatus) => {
  if (saving.value || !window.confirm(t('admin.pressure5hPage.exemptConfirm', { user: user.user_id }))) return
  saving.value = true
  try {
    await setDynamic5hUserPolicy(user.user_id, { exempt: !user.exempt, multiplier: user.multiplier || 1 })
    await load()
  } finally { saving.value = false }
}
const resetUser = async (userId: number) => {
  if (saving.value || !window.confirm(t('admin.pressure5hPage.resetConfirm'))) return
  saving.value = true
  try {
    await resetDynamic5hUser(userId)
    await load()
  } finally { saving.value = false }
}

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
