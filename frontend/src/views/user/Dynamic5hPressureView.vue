<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('dynamic5h.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('dynamic5h.description') }}</p>
        </div>
        <button class="btn-secondary inline-flex h-9 w-9 items-center justify-center" :disabled="loading" :title="t('dynamic5h.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
        </button>
      </header>

      <div v-if="loading && !status" class="flex justify-center py-16"><LoadingSpinner /></div>
      <div v-else-if="status" class="space-y-6">
        <div v-if="!status.enabled" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('dynamic5h.disabled') }}</div>
        <div v-else-if="!status.window_started_at" class="rounded-lg border border-gray-200 bg-white p-5 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">{{ t('dynamic5h.unavailable') }}</div>
        <template v-else>
          <section class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800">
            <div class="mb-6 flex flex-wrap items-center justify-between gap-3">
              <span :class="stateClass" class="rounded-full px-3 py-1 text-sm font-semibold">{{ stateLabel }}</span>
              <span v-if="status.currently_limited" class="rounded-full bg-red-100 px-3 py-1 text-sm font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-300">{{ t('dynamic5h.limited') }}</span>
              <span v-else class="rounded-full bg-emerald-100 px-3 py-1 text-sm font-semibold text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{{ t('dynamic5h.available') }}</span>
            </div>
            <Dynamic5hUsageBar :label="t('dynamic5h.usage')" :value="status.usage_percent" />
            <p class="mt-3 text-sm font-medium" :class="warningClass">{{ t(`dynamic5h.warning.${status.warning_level || 'normal'}`) }}</p>
            <div class="mt-6 grid gap-4 sm:grid-cols-2">
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-dark-700/50">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('dynamic5h.remaining') }}</p>
                <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ status.remaining_percent.toFixed(1) }}%</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-dark-700/50">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('dynamic5h.recover') }}</p>
                <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{{ formatTime(status.recover_at) }}</p>
              </div>
            </div>
            <div class="mt-4 flex flex-wrap gap-4 text-xs text-gray-500 dark:text-gray-400">
              <span>{{ t('dynamic5h.multiplier', { value: status.multiplier || 1 }) }}</span>
              <span v-if="status.exempt">{{ t('dynamic5h.exempt') }}</span>
              <span v-if="status.pending_percent > 0">{{ t('dynamic5h.pending', { value: status.pending_percent.toFixed(1) }) }}</span>
            </div>
          </section>
          <section class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800">
            <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('dynamic5h.rulesTitle') }}</h2>
            <ul class="mt-3 list-disc space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-gray-300">
              <li>{{ t('dynamic5h.ruleNormal') }}</li>
              <li>{{ t('dynamic5h.rulePeak') }}</li>
              <li>{{ t('dynamic5h.ruleDynamic') }}</li>
              <li>{{ t('dynamic5h.ruleRecover') }}</li>
            </ul>
          </section>
        </template>
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
import { getDynamic5hPressure } from '@/api/user'
import type { Dynamic5hUserStatus } from '@/types'

const { t } = useI18n()
const status = ref<Dynamic5hUserStatus | null>(null)
const loading = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const stateLabel = computed(() => t(`dynamic5h.${status.value?.state === 'peak' ? 'peak' : 'normal'}`))
const stateClass = computed(() => status.value?.state === 'peak' ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300' : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300')
const warningClass = computed(() => ({ limited: 'text-red-600', borrowed: 'text-amber-600', warning: 'text-amber-500', normal: 'text-emerald-600' }[status.value?.warning_level || 'normal']))
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : t('dynamic5h.now')
const load = async () => { loading.value = true; try { status.value = await getDynamic5hPressure() } finally { loading.value = false } }

onMounted(() => { void load(); timer = setInterval(() => void load(), 30_000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
