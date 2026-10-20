<template>
  <div class="min-w-0">
    <div class="mb-2 flex items-center justify-between gap-3">
      <span class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ label }}</span>
      <span :class="textClass" class="shrink-0 text-sm font-semibold">{{ displayPercent }}</span>
    </div>
    <div
      class="h-2.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"
      role="progressbar"
      :aria-label="label"
      :aria-valuenow="Math.min(100, normalized)"
      aria-valuemin="0"
      aria-valuemax="100"
    >
      <div :class="barClass" :style="{ width: barWidth }" class="h-full rounded-full transition-all duration-300" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ label: string; value: number }>()

const normalized = computed(() => Math.max(0, Number(props.value) || 0))
const barWidth = computed(() => `${Math.min(100, normalized.value)}%`)
const displayPercent = computed(() => `${normalized.value.toFixed(1)}%`)
const barClass = computed(() => normalized.value >= 100 ? 'bg-red-500' : normalized.value >= 75 ? 'bg-amber-500' : 'bg-emerald-500')
const textClass = computed(() => normalized.value >= 100 ? 'text-red-600 dark:text-red-400' : normalized.value >= 75 ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400')
</script>
