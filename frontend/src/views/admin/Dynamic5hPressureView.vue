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
              <div v-for="user in overview.users" :key="user.user_id" class="grid gap-4 px-5 py-4 lg:grid-cols-[minmax(110px,0.7fr)_minmax(280px,2fr)_110px_180px_100px] lg:items-center">
                <span class="font-medium text-gray-900 dark:text-white">User #{{ user.user_id }}</span>
                <Dynamic5hUsageBar :label="t('admin.pressure5hPage.usage')" :value="user.usage_percent" />
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.remaining') }}</p><p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ user.remaining_percent.toFixed(1) }}%</p></div>
                <div><p class="text-xs text-gray-400">{{ t('admin.pressure5hPage.recover') }}</p><p class="text-sm text-gray-700 dark:text-gray-200">{{ formatTime(user.recover_at) }}</p></div>
                <span :class="user.currently_limited ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300' : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'" class="w-fit rounded-full px-2.5 py-1 text-xs font-semibold">{{ t(`dynamic5h.${user.currently_limited ? 'limited' : 'available'}`) }}</span>
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
import { getDynamic5hPressureOverview } from '@/api/admin/dashboard'
import type { Dynamic5hAdminOverview } from '@/types'

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

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
