<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <div v-if="pressure?.limit_active" :class="pressure.currently_limited ? 'border-red-300 bg-red-50 dark:border-red-800 dark:bg-red-900/20' : 'border-amber-300 bg-amber-50 dark:border-amber-800 dark:bg-amber-900/20'" class="rounded-lg border p-4" data-testid="dynamic-5h-pressure-user">
          <p class="font-semibold text-gray-900 dark:text-white">{{ t(`dashboard.pressure5h.${pressure.currently_limited ? 'limited' : 'peak'}`) }}</p>
          <div class="mt-3 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
            <div class="h-full rounded-full bg-amber-500 transition-all" :style="{ width: `${Math.min(100, pressure.usage_percent)}%` }" />
          </div>
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-sm text-gray-600 dark:text-gray-300">
            <span>{{ t('dashboard.pressure5h.usage', { value: pressure.usage_percent.toFixed(1) }) }}</span>
            <span>{{ t('dashboard.pressure5h.remaining', { value: pressure.remaining_percent.toFixed(1) }) }}</span>
            <span v-if="pressure.recover_at">{{ t('dashboard.pressure5h.recover', { time: new Date(pressure.recover_at).toLocaleString() }) }}</span>
          </div>
        </div>
        <UserDashboardStats :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'; import { useI18n } from 'vue-i18n'; import { useAuthStore } from '@/stores/auth'; import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'; import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'; import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'; import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem, Dynamic5hUserStatus } from '@/types'
import { getMyPlatformQuotas, getDynamic5hPressure } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'

const authStore = useAuthStore(); const user = computed(() => authStore.user)
const { t } = useI18n()
const stats = ref<UserStatsType | null>(null); const loading = ref(false); const loadingUsage = ref(false); const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([]); const modelStats = ref<ModelStat[]>([]); const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const pressure = ref<Dynamic5hUserStatus | null>(null)
let pressureTimer: ReturnType<typeof setInterval> | null = null

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000))); const endDate = ref(formatDateLocalInput(new Date())); const granularity = ref('day')

const loadStats = async () => { loading.value = true; try { await authStore.refreshUser(); stats.value = await usageAPI.getDashboardStats() } catch (error) { console.error('Failed to load dashboard stats:', error) } finally { loading.value = false } }
const loadCharts = async () => { loadingCharts.value = true; try { const res = await Promise.all([usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }), usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value })]); trendData.value = res[0].trend || []; modelStats.value = res[1].models || [] } catch (error) { console.error('Failed to load charts:', error) } finally { loadingCharts.value = false } }
const loadRecent = async () => { loadingUsage.value = true; try { const res = await usageAPI.getByDateRange(startDate.value, endDate.value); recentUsage.value = res.items.slice(0, 5) } catch (error) { console.error('Failed to load recent usage:', error) } finally { loadingUsage.value = false } }
const loadPlatformQuotas = async () => { try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] } catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] } }
const loadPressure = async () => { try { pressure.value = await getDynamic5hPressure() } catch (error) { console.warn('Failed to load dynamic 5h pressure:', error) } }
const refreshAll = () => { loadStats(); loadCharts(); loadRecent(); loadPlatformQuotas(); loadPressure() }

onMounted(() => { refreshAll(); pressureTimer = setInterval(loadPressure, 30000) })
onBeforeUnmount(() => { if (pressureTimer) clearInterval(pressureTimer) })
</script>
