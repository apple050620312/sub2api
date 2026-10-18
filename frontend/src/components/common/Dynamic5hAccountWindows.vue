<template>
  <section class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
      <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('dynamic5h.accountWindows') }}</h2>
      <input v-model="search" class="input w-full sm:max-w-sm" :placeholder="t('dynamic5h.searchAccounts')" :aria-label="t('dynamic5h.searchAccounts')" />
    </div>
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700/50"><tr>
          <th class="px-5 py-3">{{ t('admin.pressure5hPage.account') }}</th>
          <th class="px-5 py-3">{{ t('admin.pressure5hPage.status') }}</th>
          <th class="px-5 py-3">5h</th><th class="px-5 py-3">7d</th>
          <th class="px-5 py-3">{{ t('admin.pressure5hPage.rejoin') }}</th>
        </tr></thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="account in filtered" :key="account.account_id">
            <td class="px-5 py-3"><p class="font-medium text-gray-900 dark:text-white">{{ account.name }}</p><p class="text-xs text-gray-400">{{ account.platform }}</p></td>
            <td class="px-5 py-3"><span :class="account.included ? 'text-emerald-600' : 'text-amber-600'">{{ t(`admin.pressure5hPage.accountReasons.${account.reason}`) }}</span></td>
            <td class="min-w-48 px-5 py-3"><Dynamic5hUsageBar v-if="account.five_hour_used_percent !== undefined" label="5h" :value="account.five_hour_used_percent" /><span v-else>{{ t('dynamic5h.windowUnavailable') }}</span><p class="mt-1 text-xs text-gray-500">{{ formatReset(account.five_hour_used_percent, account.five_hour_reset_at) }}</p></td>
            <td class="min-w-48 px-5 py-3"><Dynamic5hUsageBar v-if="account.seven_day_used_percent !== undefined" label="7d" :value="account.seven_day_used_percent" /><span v-else>{{ t('dynamic5h.windowUnavailable') }}</span><p class="mt-1 text-xs text-gray-500">{{ formatReset(account.seven_day_used_percent, account.seven_day_reset_at) }}</p></td>
            <td class="px-5 py-3">{{ account.rejoin_at ? new Date(account.rejoin_at).toLocaleString() : '-' }}</td>
          </tr>
          <tr v-if="filtered.length === 0"><td colspan="5" class="p-5 text-gray-500">{{ t('dynamic5h.noAccounts') }}</td></tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Dynamic5hAdminOverview } from '@/types'
import Dynamic5hUsageBar from './Dynamic5hUsageBar.vue'

const props = defineProps<{ accounts: Dynamic5hAdminOverview['accounts'] }>()
const { t } = useI18n()
const search = ref('')
const filtered = computed(() => props.accounts.filter(account => `${account.name} ${account.platform} ${account.account_id}`.toLowerCase().includes(search.value.trim().toLowerCase())))
const formatReset = (used?: number, reset?: string) => reset ? new Date(reset).toLocaleString() : used === 0 ? t('common.now') : '-'
</script>
