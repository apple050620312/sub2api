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

          <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
              <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.pressure5hPage.users') }}</h2>
            </div>
            <div v-if="!overview.pool.calibration_ready" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.unavailable') }}</div>
            <div v-else-if="overview.users.length === 0" class="p-6 text-sm text-gray-500">{{ t('admin.pressure5hPage.noUsers') }}</div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div v-for="user in overview.users" :key="user.user_id" class="grid gap-4 px-5 py-4 lg:grid-cols-[minmax(160px,0.8fr)_minmax(260px,2fr)_100px_170px_minmax(250px,auto)] lg:items-center">
                <div><p class="font-medium text-gray-900 dark:text-white">{{ user.email || `用户 #${user.user_id}` }}</p><p class="text-xs text-gray-400">ID: {{ user.user_id }}</p></div>
                <Dynamic5hUsageBar :label="t('admin.pressure5hPage.usage')" :value="user.usage_percent" />
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.remaining') }}</p><p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ user.remaining_percent.toFixed(1) }}%</p></div>
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.recover') }}</p><p class="text-sm text-gray-700 dark:text-gray-200">{{ formatTime(user.recover_at) }}</p></div>
                <div class="flex flex-wrap items-center gap-2">
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
let timer: ReturnType<typeof setInterval> | undefined

const metrics = computed(() => overview.value ? [
  { label: t('admin.pressure5hPage.pressure'), value: `${(overview.value.pool.pressure * 100).toFixed(1)}% (${overview.value.pool.state === 'peak' ? 'Peak' : 'Normal'})` },
  { label: t('admin.pressure5hPage.effectiveAccounts'), value: overview.value.pool.account_count },
  { label: t('admin.pressure5hPage.activeUsers'), value: overview.value.pool.active_user_count },
  { label: t('admin.pressure5hPage.enforcement'), value: t(`admin.pressure5hPage.${overview.value.pool.peak_active && overview.value.pool.calibration_ready ? 'on' : 'off'}`) },
] : [])
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : '-'
const load = async () => { loading.value = true; try { overview.value = await getDynamic5hPressureOverview() } finally { loading.value = false } }
const updateMultiplier = async (user: Dynamic5hAdminUserStatus, event: Event) => {
  const multiplier = Number((event.target as HTMLInputElement).value)
  if (!Number.isFinite(multiplier) || multiplier <= 0) { await load(); return }
  await setDynamic5hUserPolicy(user.user_id, { exempt: user.exempt, multiplier })
  await load()
}
const toggleExempt = async (user: Dynamic5hAdminUserStatus) => {
  await setDynamic5hUserPolicy(user.user_id, { exempt: !user.exempt, multiplier: user.multiplier || 1 })
  await load()
}
const resetUser = async (userId: number) => { await resetDynamic5hUser(userId); await load() }

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
