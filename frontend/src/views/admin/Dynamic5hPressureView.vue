<template>
  <AppLayout>
    <div class="space-y-6">
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
      <template v-else-if="overview">
        <div v-if="!overview.pool.enabled" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('admin.pressure5hPage.disabled') }}</div>
        <template v-else>
          <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <div v-for="metric in metrics" :key="metric.label" class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ metric.label }}</p>
              <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ metric.value }}</p>
            </div>
          </section>
          <div v-if="overview.guaranteed_capacity_ratio > 1" class="rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-200">
            {{ t('admin.pressure5hPage.overcommitted', { value: (overview.guaranteed_capacity_ratio * 100).toFixed(1) }) }}
          </div>

          <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
              <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.users') }}</h2>
            </div>
            <div v-if="!overview.pool.calibration_ready" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.unavailable') }}</div>
            <div v-else-if="overview.users.length === 0" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.noUsers') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div v-for="user in overview.users" :key="user.user_id" class="grid gap-4 px-5 py-4 lg:grid-cols-[minmax(160px,0.8fr)_minmax(260px,2fr)_100px_170px_minmax(250px,auto)] lg:items-center">
                <div><div class="flex flex-wrap items-center gap-2"><p class="font-medium text-gray-900 dark:text-white">{{ user.email || `用户 #${user.user_id}` }}</p><span class="rounded px-1.5 py-0.5 text-xs font-medium" :class="user.active ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'">{{ t(`admin.pressure5hPage.${user.active ? 'active' : 'inactive'}`) }}</span></div><p class="text-xs text-gray-400">ID: {{ user.user_id }}</p></div>
                <Dynamic5hUsageBar :label="t('admin.pressure5hPage.usage')" :value="user.usage_percent" />
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.remaining') }}</p><p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ user.remaining_percent.toFixed(1) }}%</p></div>
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.recover') }}</p><p class="text-sm text-gray-700 dark:text-gray-200">{{ formatTime(user.recover_at) }}</p></div>
                <div class="flex flex-wrap items-center gap-2">
                  <input v-model="policyReasons[user.user_id]" class="input h-8 w-28 px-2 text-xs" :placeholder="t('admin.pressure5hPage.reason')" />
                  <label class="flex h-8 items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.pressure5hPage.multiplier') }}
                    <input class="input h-8 w-20 px-2 text-xs" type="number" min="0.01" step="0.01" :value="user.multiplier || 1" @change="updateMultiplier(user, $event)" />
                  </label>
                  <button class="h-8 px-2 text-xs" :class="user.exempt ? 'btn-primary' : 'btn-secondary'" :aria-pressed="user.exempt" @click="toggleExempt(user)">{{ t('admin.pressure5hPage.exempt') }}</button>
                  <button class="btn-secondary h-8 px-2 text-xs" @click="resetUser(user.user_id)">{{ t('admin.pressure5hPage.resetUsage') }}</button>
                </div>
              </div>
            </div>
          </section>

          <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700"><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.accountDiagnostics') }}</h2></div>
            <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700/50"><tr><th class="px-5 py-3">{{ t('admin.pressure5hPage.account') }}</th><th class="px-5 py-3">{{ t('admin.pressure5hPage.status') }}</th><th class="px-5 py-3">5h</th><th class="px-5 py-3">7d</th><th class="px-5 py-3">{{ t('admin.pressure5hPage.rejoin') }}</th></tr></thead><tbody class="divide-y divide-gray-100 dark:divide-dark-700"><tr v-for="account in overview.accounts" :key="account.account_id"><td class="px-5 py-3"><p class="font-medium text-gray-900 dark:text-white">{{ account.name }}</p><p class="text-xs text-gray-400">{{ account.platform }} · ID {{ account.account_id }}</p></td><td class="px-5 py-3"><span :class="account.included ? 'text-emerald-600' : 'text-amber-600'">{{ t(`admin.pressure5hPage.accountReasons.${account.reason}`) }}</span></td><td class="px-5 py-3">{{ formatWindow(account.five_hour_used_percent, account.five_hour_reset_at) }}</td><td class="px-5 py-3">{{ formatWindow(account.seven_day_used_percent, account.seven_day_reset_at) }}</td><td class="px-5 py-3">{{ formatTime(account.rejoin_at) }}</td></tr></tbody></table></div>
          </section>

          <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700"><h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.audit') }}</h2></div>
            <div v-if="overview.audit_logs.length === 0" class="p-5 text-sm text-gray-500">{{ t('admin.pressure5hPage.noAudit') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700"><div v-for="log in overview.audit_logs" :key="log.id" class="grid gap-2 px-5 py-3 text-sm md:grid-cols-[170px_1fr_1fr_2fr]"><span>{{ formatTime(log.created_at) }}</span><span>{{ log.actor_email || `ID ${log.actor_user_id || '-'}` }}</span><span>{{ t(`admin.pressure5hPage.auditActions.${log.action}`) }} · User {{ log.user_id }}</span><span class="text-gray-500">{{ log.reason }}</span></div></div>
          </section>
        </template>
      </template>
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
const policyReasons = ref<Record<number, string>>({})
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
const formatWindow = (used?: number, reset?: string) => used === undefined ? '-' : `${used.toFixed(1)}% · ${formatTime(reset)}`
const load = async () => { loading.value = true; try { overview.value = await getDynamic5hPressureOverview() } finally { loading.value = false } }
const updateMultiplier = async (user: Dynamic5hAdminUserStatus, event: Event) => {
  const multiplier = Number((event.target as HTMLInputElement).value)
  const reason = policyReasons.value[user.user_id]?.trim()
  if (!Number.isFinite(multiplier) || multiplier <= 0 || !reason) { await load(); return }
  await setDynamic5hUserPolicy(user.user_id, { exempt: user.exempt, multiplier, reason })
  policyReasons.value[user.user_id] = ''
  await load()
}
const toggleExempt = async (user: Dynamic5hAdminUserStatus) => {
  const reason = policyReasons.value[user.user_id]?.trim()
  if (!reason) return
  await setDynamic5hUserPolicy(user.user_id, { exempt: !user.exempt, multiplier: user.multiplier || 1, reason })
  policyReasons.value[user.user_id] = ''
  await load()
}
const resetUser = async (userId: number) => {
  const reason = policyReasons.value[userId]?.trim()
  if (!reason || !window.confirm(t('admin.pressure5hPage.resetConfirm'))) return
  await resetDynamic5hUser(userId, reason)
  policyReasons.value[userId] = ''
  await load()
}

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
