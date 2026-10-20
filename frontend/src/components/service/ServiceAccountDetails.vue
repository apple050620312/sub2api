<template>
  <BaseDialog :show="!!account" :title="account?.name || '账号详情'" width="normal" @close="$emit('close')">
    <div v-if="account" class="space-y-6">
      <p class="break-all text-sm text-gray-500">{{ account.email || '未提供邮箱' }} · {{ serviceAccountPlanLabel(account.plan_type) }}</p>
      <dl class="grid grid-cols-1 gap-x-6 gap-y-5 text-sm sm:grid-cols-2">
        <div v-for="item in details" :key="item.label"><dt class="text-gray-500">{{ item.label }}</dt><dd class="mt-1 break-words font-medium text-gray-900 dark:text-gray-100">{{ item.value }}</dd></div>
      </dl>
      <div v-for="window in windows" :key="window.label" class="space-y-2">
        <UsageWindowBar :label="window.label" :value="window.value || 0" />
        <p class="text-xs text-gray-500">{{ window.value === undefined ? '暂无上游用量数据' : `重置时间：${time(window.reset)}` }}</p>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import UsageWindowBar from './UsageWindowBar.vue'
import type { ServiceAccountSummary } from '@/api/serviceCenter'
import { serviceAccountPlanLabel } from '@/utils/serviceCenter'

const props = defineProps<{ account: ServiceAccountSummary | null }>()
defineEmits<{ close: [] }>()
const time = (value?: string) => value ? new Date(value).toLocaleString() : '暂无'
const details = computed(() => props.account ? [
  { label: '运行状态', value: props.account.paused ? '已暂停' : ({ active: '正常', inactive: '已停用', disabled: '已停用', error: '认证或运行异常' }[props.account.status] || props.account.status) },
  { label: '调度状态', value: props.account.schedulable ? '允许调度' : '已由管理员停用' },
  { label: '并发数', value: props.account.concurrency },
  { label: '订阅分组', value: props.account.groups.join('、') || '暂未分配' },
  { label: '添加时间', value: time(props.account.created_at) },
  { label: '更新时间', value: time(props.account.updated_at) },
  { label: '最近使用', value: time(props.account.last_used_at) },
  { label: '账号有效期', value: props.account.expires_at ? time(props.account.expires_at) : '未设置到期时间' },
  { label: 'OAuth 令牌到期', value: time(props.account.auth_expires_at) },
  { label: '上游限流恢复', value: time(props.account.rate_limit_reset_at) },
  { label: '临时暂停截止', value: time(props.account.blocked_until) },
] : [])
const windows = computed(() => [
  { label: '上游五小时窗口', value: props.account?.usage.five_hour_used_percent, reset: props.account?.usage.five_hour_reset_at },
  { label: '上游七天窗口', value: props.account?.usage.seven_day_used_percent, reset: props.account?.usage.seven_day_reset_at },
])
</script>
