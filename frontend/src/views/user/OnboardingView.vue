<template>
    <div class="w-full space-y-8">
      <header class="card p-6">
        <p class="text-sm font-semibold text-primary-600 dark:text-primary-400">新用户引导</p>
        <h2 class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">接入并开始使用</h2>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">按顺序完成下面四步。你的邮箱和 OAuth 令牌不会显示给其他用户。</p>
      </header>

      <div v-if="loading" class="card flex items-center justify-center gap-3 p-8 text-gray-500"><LoadingSpinner size="sm" />正在加载引导状态…</div>
      <template v-else>
        <section class="card p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第一步</p>
              <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">绑定 Discord</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">授权后自动加入指定服务器并获得身份组。</p>
            </div>
            <StatusBadge :status="overview?.discord?.status === 'active' ? 'active' : 'inactive'" :label="overview?.discord?.status === 'active' ? '已绑定' : '未完成'" />
          </div>
          <p v-if="overview?.discord" class="mt-4 text-sm text-gray-600 dark:text-gray-300">Discord：{{ overview.discord.discord_username }}</p>
          <button class="btn btn-primary mt-4" :disabled="busy" @click="startDiscord">{{ overview?.discord ? '重新绑定 Discord' : '绑定 Discord' }}</button>
        </section>

        <section class="card p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第二步</p>
              <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">添加自己的 OpenAI OAuth 账号</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">只允许 Plus、Pro 或 Business 账号，账号会优先服务你自己。</p>
            </div>
            <StatusBadge :status="overview?.account_units ? 'active' : 'inactive'" :label="overview?.accounts.length ? `${overview.account_units} 个 Plus 等值` : '未添加'" />
          </div>
          <div class="mt-4 flex flex-wrap gap-3">
            <button class="btn btn-primary" :disabled="busy || !overview?.ready_for_account" @click="startOpenAI">打开 OpenAI 授权</button>
            <span v-if="!overview?.ready_for_account" class="self-center text-sm text-amber-600">请先完成 Discord 绑定。</span>
          </div>
          <div v-if="oauthStart" class="mt-5 space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700/50">
            <p class="text-sm text-gray-700 dark:text-gray-200">授权完成后，把回调地址粘贴到下面，系统会自动提取 code 和 state。</p>
            <a class="break-all text-sm text-primary-600 hover:underline" :href="oauthStart.auth_url" target="_blank" rel="noreferrer">重新打开授权页面</a>
            <textarea v-model="callbackURL" class="input min-h-20 w-full" placeholder="粘贴 http://localhost:1455/auth/callback?code=...&state=..."></textarea>
            <button class="btn btn-primary" :disabled="busy || !callbackURL || !oauthSessionID" @click="completeOpenAI">提交回调并绑定账号</button>
          </div>
          <div class="mt-6">
            <DataTable :columns="accountColumns" :data="overview?.accounts || []" row-key="id" :actions-count="3">
              <template #cell-name="{ row }"><p class="font-medium">{{ row.name }}</p><p class="mt-1 text-xs text-gray-500">{{ row.email || '未提供邮箱' }}</p></template>
              <template #cell-plan_type="{ value }">{{ serviceAccountPlanLabel(value) }}</template>
              <template #cell-status="{ row }"><StatusBadge :status="row.paused ? 'inactive' : row.status" :label="row.paused ? '已暂停' : row.status === 'active' ? '正常' : row.status === 'error' ? '需要处理' : '已停用'" /></template>
              <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" @click="detail = row">详情</button><button class="btn btn-secondary btn-sm" :disabled="busy || (!row.paused && row.status !== 'active')" @click="askAccountAction(row, row.paused ? 'resume' : 'pause')">{{ row.paused ? '恢复' : '暂停' }}</button><button class="btn btn-danger btn-sm" :disabled="busy" @click="askAccountAction(row, 'delete')">删除</button></div></template>
            </DataTable>
            <p class="mt-4 text-sm leading-6 text-gray-500">账号优先服务你自己，忙碌时自动使用公共池。暂停或删除账号会重新计算自动订阅资格；恢复账号不会重置用量或延长原有效期。</p>
          </div>
          <div v-if="overview?.subscriptions?.length" class="mt-6 space-y-3 border-t border-gray-100 pt-6 dark:border-dark-700">
            <h3 class="font-medium">当前订阅</h3><div v-for="subscription in overview.subscriptions" :key="subscription.group_name" class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>{{ subscription.group_name }}</span><StatusBadge :status="subscription.status" :label="subscription.status === 'active' ? '有效' : subscription.status === 'suspended' ? '已暂停' : '已到期'" /><span class="text-gray-500">到期：{{ new Date(subscription.expires_at).toLocaleString() }}</span></div>
          </div>
        </section>

        <section class="card p-6" aria-label="第三步：API Key">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div><p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第三步</p><h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">API Key</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">已有可用 Key 时直接复用，下一步将使用你选择的 Key。</p></div>
            <StatusBadge :status="selectedKey ? 'active' : 'inactive'" :label="selectedKey ? '已完成' : '未完成'" />
          </div>
          <div v-if="keyLoadError" class="mt-4 flex flex-wrap items-center gap-3 text-sm text-red-600" role="alert">{{ keyLoadError }}<button class="btn btn-secondary btn-sm" @click="load">重试</button></div>
          <div v-else-if="existingKeys.length" class="mt-5 space-y-3">
            <label class="input-label block">用于 CC Switch 的 API Key</label>
            <Select v-model="selectedKeyID" :options="keyOptions" searchable aria-label="用于 CC Switch 的 API Key" />
            <p v-if="selectedKey" class="text-sm text-gray-500">已选择“{{ selectedKey.name }}” · {{ keyGroupName(selectedKey) }}，无需重复创建。</p>
            <p v-else class="text-sm text-amber-600">已有 Key 当前不可用于 OpenAI，请检查对应分组、有效期和额度，或创建新的 Key。</p>
            <button class="btn btn-secondary btn-sm" @click="showKeyForm = !showKeyForm">{{ showKeyForm ? '收起创建表单' : '创建另一个 API Key' }}</button>
          </div>
          <div v-if="!keyLoadError && (!existingKeys.length || showKeyForm)" class="mt-5 space-y-4 rounded-lg bg-gray-50 p-4 dark:bg-dark-900">
            <div class="grid gap-4 sm:grid-cols-2"><Input v-model="keyName" label="Key 名称" placeholder="例如：我的 Codex" id="service-key-name" /><div><label class="input-label mb-1.5 block">订阅分组</label><Select v-model="keyGroupID" :options="keyGroups.map(group => ({ value: group.id, label: group.name }))" aria-label="API Key 订阅分组" /></div></div>
            <button class="btn btn-primary" :disabled="busy || !overview?.ready_for_key || !keyName.trim() || !keyGroupID" @click="createKey">创建 API Key</button>
            <p v-if="!overview?.ready_for_key" class="text-sm text-amber-600">请先完成 Discord 绑定，并确认已有有效的 OpenAI 订阅。</p>
          </div>
          <p v-if="overview?.ready_for_key && !keyGroups.length" class="mt-3 text-sm text-amber-600">暂无可用的 OpenAI 分组，请联系管理员配置订阅分配。</p>
          <div v-if="createdKey" class="mt-4 rounded-xl border border-emerald-200 bg-emerald-50 p-4 dark:border-emerald-800 dark:bg-emerald-900/20">
            <p class="text-sm font-semibold text-emerald-800 dark:text-emerald-200">请立即复制并保存：</p>
            <code class="mt-2 block break-all text-sm text-emerald-900 dark:text-emerald-100">{{ createdKey }}</code>
          </div>
        </section>

        <section class="card p-6" aria-label="第四步：CC Switch">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第四步</p>
          <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">下载并配置 CC Switch</h2>
          <ol class="mt-4 list-decimal space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-gray-300">
            <li>下载并安装 CC Switch。</li>
            <li>点击“导入至 CCS”，选择导入的来源并切换到它。</li>
            <li>重启 Codex，使新的 API 来源生效。</li>
          </ol>
          <p v-if="selectedKey" class="mt-4 text-sm text-gray-600 dark:text-gray-300">本次导入：<strong>{{ selectedKey.name }}</strong> · {{ keyGroupName(selectedKey) }}</p>
          <div class="mt-4 flex flex-wrap gap-3">
            <a v-if="overview?.config.ccswitch_download_url" class="btn btn-secondary" :href="overview.config.ccswitch_download_url" target="_blank" rel="noreferrer">下载 CC Switch</a>
            <a v-if="importURL" class="btn btn-primary" :href="importURL">导入至 CCS</a>
          </div>
          <p v-if="!selectedKey" class="mt-3 text-sm text-amber-600">请在第三步选择可用的 API Key，或创建一个新的 Key。</p>
        </section>
      </template>
      <ServiceAccountDetails :account="detail" @close="detail = null" />
      <ConfirmDialog :show="!!confirmation" :title="confirmation?.title || ''" :message="confirmation?.message || ''" :danger="confirmation?.action === 'delete'" @cancel="confirmation = null" @confirm="confirmAccountAction" />
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { buildGatewayUrl } from '@/api/client'
import {
  completeDiscordOnboarding,
  completeOpenAIOnboarding,
  getOnboardingOverview,
  startDiscordOnboarding,
  startOpenAIOnboarding,
  setOnboardingAccountPaused,
  deleteOnboardingAccount,
  type OnboardingAccountSummary,
  type OnboardingOAuthStart,
  type OnboardingUserOverview,
} from '@/api/user'
import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import type { ApiKey, Group } from '@/types'
import { serviceAccountPlanLabel, serviceKeyUnavailableReason } from '@/utils/serviceCenter'
import { buildCcSwitchImportDeeplink, CC_SWITCH_USAGE_SCRIPT } from '@/utils/ccswitchImport'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import Input from '@/components/common/Input.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ServiceAccountDetails from '@/components/common/ServiceAccountDetails.vue'
import { useAppStore } from '@/stores/app'

const route = useRoute()
const router = useRouter()
const overview = ref<OnboardingUserOverview | null>(null)
const loading = ref(true)
const busy = ref(false)
const oauthStart = ref<OnboardingOAuthStart | null>(null)
const oauthSessionID = ref('')
const callbackURL = ref('')
const keyName = ref('我的 Codex')
const keyGroups = ref<Group[]>([])
const keyGroupID = ref<number>()
const createdKey = ref('')
const existingKeys = ref<ApiKey[]>([])
const selectedKeyID = ref<number>()
const showKeyForm = ref(false)
const keyLoadError = ref('')
const selectedKey = computed(() => existingKeys.value.find(key => key.id === selectedKeyID.value && !serviceKeyUnavailableReason(key, keyGroups.value)))
const keyGroupName = (key: ApiKey) => keyGroups.value.find(group => group.id === key.group_id)?.name || key.group?.name || '分组不可用'
const keyOptions = computed(() => existingKeys.value.map(key => {
  const reason = serviceKeyUnavailableReason(key, keyGroups.value)
  return { value: key.id, label: `${key.name} · ${keyGroupName(key)}${reason ? ` · ${reason}` : ''}`, disabled: !!reason }
}))
const app = useAppStore()
const detail = ref<OnboardingAccountSummary | null>(null)
const accountColumns = [{ key: 'name', label: 'GPT 账号' }, { key: 'plan_type', label: '套餐' }, { key: 'status', label: '状态' }, { key: 'actions', label: '操作' }]
const confirmation = ref<{ account: OnboardingAccountSummary; action: 'pause' | 'resume' | 'delete'; title: string; message: string } | null>(null)
const reportError = (error: unknown) => app.showError((error as { response?: { data?: { message?: string } } })?.response?.data?.message || (error instanceof TypeError ? '回调链接格式无效，请粘贴完整的回调地址' : '操作失败，请稍后重试'))
function askAccountAction(account: OnboardingAccountSummary, action: 'pause' | 'resume' | 'delete') {
  const label = { pause: '暂停', resume: '恢复', delete: '删除' }[action]
  confirmation.value = { account, action, title: `确认${label}账号`, message: `确定${label}“${account.name}”（${account.email || serviceAccountPlanLabel(account.plan_type)}）？自动订阅资格会同步更新。${action === 'delete' ? '删除后需要重新授权才能添加。' : '订阅用量和原有效期保持不变。'}` }
}
async function confirmAccountAction() {
  const pending = confirmation.value
  if (!pending || busy.value) return
  confirmation.value = null
  busy.value = true
  try {
    if (pending.action === 'delete') await deleteOnboardingAccount(pending.account.id)
    else await setOnboardingAccountPaused(pending.account.id, pending.action === 'pause')
    await load()
    app.showSuccess('账号及订阅已更新')
  } catch (error) { reportError(error) } finally { busy.value = false }
}

const redirectURI = computed(() => `${window.location.origin}/onboarding`)
const importURL = computed(() => {
  if (!selectedKey.value) return ''
  return buildCcSwitchImportDeeplink({
    baseUrl: buildGatewayUrl(''),
    clientType: 'gemini',
    providerName: 'Sub2API Codex',
    apiKey: selectedKey.value.key,
    usageScript: CC_SWITCH_USAGE_SCRIPT,
    platform: 'openai',
  })
})

function queryValue(name: string): string {
  const value = route.query[name]
  return Array.isArray(value) ? String(value[0] || '') : String(value || '')
}

async function load() {
  loading.value = true
  try {
    overview.value = await getOnboardingOverview()
    keyGroups.value = (await userGroupsAPI.getAvailable()).filter(group => group.platform === 'openai' && group.status === 'active')
    if (!keyGroups.value.some(group => group.id === keyGroupID.value)) {
      keyGroupID.value = (keyGroups.value.find(group => group.subscription_type === 'subscription') || keyGroups.value[0])?.id
    }
    keyLoadError.value = ''
    try {
      const keys: ApiKey[] = []
      for (let page = 1; ; page++) {
        const batch = await keysAPI.list(page, 100)
        keys.push(...batch.items)
        if (!batch.items.length || keys.length >= batch.total) break
      }
      existingKeys.value = keys.filter(key => key.group?.platform === 'openai' || keyGroups.value.some(group => group.id === key.group_id))
      if (!selectedKey.value) selectedKeyID.value = existingKeys.value.find(key => !serviceKeyUnavailableReason(key, keyGroups.value))?.id
    } catch {
      existingKeys.value = []
      keyLoadError.value = '加载已有 API Key 失败，请重试后继续。'
    }
  } catch (error) { reportError(error) } finally {
    loading.value = false
  }
}

async function startDiscord() {
  busy.value = true
  try {
    const result = await startDiscordOnboarding(redirectURI.value)
    window.location.href = result.auth_url
  } catch (error) { reportError(error) } finally {
    busy.value = false
  }
}

async function completeDiscord() {
  const code = queryValue('code')
  const state = queryValue('state')
  if (!code || !state) return
  busy.value = true
  try {
    await completeDiscordOnboarding({ code, state, redirect_uri: redirectURI.value })
    await router.replace('/service-center')
    await load()
  } catch (error) { reportError(error) } finally {
    busy.value = false
  }
}

async function startOpenAI() {
  busy.value = true
  const popup = window.open('about:blank', '_blank')
  try {
    oauthStart.value = await startOpenAIOnboarding(redirectURI.value)
    oauthSessionID.value = oauthStart.value.session_id
    if (popup) { popup.opener = null; popup.location.href = oauthStart.value.auth_url }
  } catch (error) { popup?.close(); reportError(error) } finally {
    busy.value = false
  }
}

async function completeOpenAI() {
  busy.value = true
  try {
    const parsed = new URL(callbackURL.value.trim())
    if (!parsed.searchParams.get('code') || !parsed.searchParams.get('state')) throw new TypeError('invalid callback')
    await completeOpenAIOnboarding({
      session_id: oauthSessionID.value,
      code: parsed.searchParams.get('code') || '',
      state: parsed.searchParams.get('state') || '',
      onboarding_state: oauthStart.value?.onboarding_state || '',
      redirect_uri: oauthStart.value?.redirect_uri || redirectURI.value,
    })
    oauthStart.value = null
    callbackURL.value = ''
    await load()
  } catch (error) { reportError(error) } finally {
    busy.value = false
  }
}

async function createKey() {
  if (!keyGroupID.value || !overview.value?.ready_for_key || busy.value) return
  busy.value = true
  try {
    const key = await keysAPI.create(keyName.value.trim(), keyGroupID.value)
    createdKey.value = key.key
    selectedKeyID.value = key.id
    await load()
    showKeyForm.value = false
  } catch (error) { reportError(error) } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await load()
  await completeDiscord()
})
</script>
