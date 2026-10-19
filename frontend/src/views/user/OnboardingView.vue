<template>
    <div class="w-full space-y-8">
      <header class="card p-6">
        <p class="text-sm font-semibold text-primary-600 dark:text-primary-400">新用户引导</p>
        <h2 class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">接入并开始使用</h2>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">按顺序完成下面三步。你的邮箱和 OAuth 令牌不会显示给其他用户。</p>
      </header>

      <div v-if="loading" class="card flex items-center justify-center gap-3 p-8 text-gray-500"><LoadingSpinner size="sm" />正在加载引导状态…</div>
      <template v-else>
        <section class="card p-6">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第一步</p>
              <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">添加自己的 OpenAI OAuth 账号</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">只允许 Plus、Pro 或 Business 账号，账号会优先服务你自己。</p>
            </div>
            <StatusBadge :status="overview?.account_units ? 'active' : 'inactive'" :label="overview?.accounts.length ? `${overview.account_units} 个 Plus 等值` : '未添加'" />
          </div>
          <div class="mt-4 flex flex-wrap gap-3">
            <button class="btn btn-primary" :disabled="busy || !overview?.ready_for_account" @click="startOpenAI">打开 OpenAI 授权</button>
            <span v-if="!overview?.ready_for_account" class="self-center text-sm text-amber-600">{{ overview?.account_setup_message || '账号接入暂未配置，请联系管理员。' }}</span>
          </div>
          <div v-if="oauthStart" class="mt-5 space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700/50">
            <p class="text-sm text-gray-700 dark:text-gray-200">授权完成后，把回调地址粘贴到下面，系统会自动提取 code 和 state。</p>
            <a class="break-all text-sm text-primary-600 hover:underline" :href="oauthStart.auth_url" target="_blank" rel="noreferrer">重新打开授权页面</a>
            <textarea v-model="callbackURL" class="input min-h-20 w-full" placeholder="粘贴 http://localhost:1455/auth/callback?code=...&state=..."></textarea>
            <button class="btn btn-primary" :disabled="busy || !callbackURL || !oauthSessionID" @click="completeOpenAI">提交回调并绑定账号</button>
          </div>
          <div class="mt-6">
            <DataTable :columns="accountColumns" :data="overview?.accounts || []" row-key="id" :actions-count="4">
              <template #cell-name="{ row }"><p class="font-medium">{{ row.name }}</p><p class="mt-1 text-xs text-gray-500">{{ row.email || '未提供邮箱' }}</p></template>
              <template #cell-plan_type="{ value }">{{ serviceAccountPlanLabel(value) }}</template>
              <template #cell-status="{ row }"><StatusBadge :status="row.paused ? 'inactive' : row.status" :label="row.paused ? '已暂停' : row.status === 'active' ? '正常' : row.status === 'error' ? '需要处理' : '已停用'" /></template>
              <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" @click="detail = row">详情</button><button class="btn btn-secondary btn-sm" :disabled="busy" @click="openReauthorization(row)">重新授权</button><button class="btn btn-secondary btn-sm" :disabled="busy || (!row.paused && row.status !== 'active')" @click="askAccountAction(row, row.paused ? 'resume' : 'pause')">{{ row.paused ? '恢复' : '暂停' }}</button><button class="btn btn-danger btn-sm" :disabled="busy" @click="askAccountAction(row, 'delete')">删除</button></div></template>
            </DataTable>
            <p class="mt-4 text-sm leading-6 text-gray-500">账号优先服务你自己，忙碌时自动使用公共池。暂停或删除账号会重新计算自动订阅资格；恢复账号不会重置用量或延长原有效期。</p>
          </div>
          <div v-if="overview?.subscriptions?.length" class="mt-6 space-y-3 border-t border-gray-100 pt-6 dark:border-dark-700">
            <h3 class="font-medium">当前订阅</h3><div v-for="subscription in overview.subscriptions" :key="subscription.group_name" class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>{{ subscription.group_name }}</span><StatusBadge :status="subscription.status" :label="subscription.status === 'active' ? '有效' : subscription.status === 'suspended' ? '已暂停' : '已到期'" /><span class="text-gray-500">到期：{{ new Date(subscription.expires_at).toLocaleString() }}</span></div>
          </div>
        </section>

        <section class="card p-6" aria-label="第二步：API Key">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div><p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第二步</p><h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">API Key</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">已有可用 Key 时直接复用，下一步将使用你选择的 Key。</p></div>
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
            <p v-if="!overview?.ready_for_key" class="text-sm text-amber-600">请先完成 GPT 账号接入，并确认已有有效的 OpenAI 订阅。</p>
          </div>
          <p v-if="overview?.ready_for_key && !keyGroups.length" class="mt-3 text-sm text-amber-600">暂无可用的 OpenAI 分组，请联系管理员配置订阅分配。</p>
          <div v-if="createdKey" class="mt-4 rounded-xl border border-emerald-200 bg-emerald-50 p-4 dark:border-emerald-800 dark:bg-emerald-900/20">
            <p class="text-sm font-semibold text-emerald-800 dark:text-emerald-200">请立即复制并保存：</p>
            <code class="mt-2 block break-all text-sm text-emerald-900 dark:text-emerald-100">{{ createdKey }}</code>
          </div>
        </section>

        <section class="card p-6" aria-label="第三步：CC Switch">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第三步</p>
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
          <p v-if="!selectedKey" class="mt-3 text-sm text-amber-600">请在第二步选择可用的 API Key，或创建一个新的 Key。</p>
        </section>
      </template>
      <ServiceAccountDetails :account="detail" @close="detail = null" />
      <BaseDialog v-if="reauthAccount" :show="true" :title="`重新授权：${reauthAccount.name}`" :show-close-button="!busy" :close-on-escape="!busy" @close="closeReauthorization">
        <div class="space-y-5">
          <div class="rounded-lg bg-gray-50 p-4 dark:bg-dark-900"><p class="font-medium">{{ reauthAccount.name }}</p><p class="mt-1 text-sm text-gray-500">{{ reauthAccount.email || '未提供邮箱' }} · {{ serviceAccountPlanLabel(reauthAccount.plan_type) }}</p></div>
          <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">请使用上面同一个 GPT 账号及原工作区登录。原有调用设置、暂停状态和使用记录会保留，订阅用量与有效期不会重置。</p>
          <div class="space-y-3"><p class="text-sm font-medium">1. 打开授权页面并完成登录</p><button class="btn btn-primary" :disabled="busy" @click="startReauthorization">{{ reauthStart ? '重新开始授权' : '打开 OpenAI 授权' }}</button><a v-if="reauthStart" class="block text-sm text-primary-600 hover:underline" :href="reauthStart.auth_url" target="_blank" rel="noreferrer">重新打开授权页面</a></div>
          <div v-if="reauthStart" class="space-y-3"><label for="service-account-reauth-callback" class="block text-sm font-medium">2. 粘贴完整回调地址</label><p class="text-sm leading-6 text-gray-500">跳转到 localhost 后，即使页面无法打开，也可以复制浏览器地址栏中的完整链接。</p><textarea id="service-account-reauth-callback" v-model="reauthCallbackURL" :disabled="busy" class="input min-h-24 w-full" placeholder="粘贴 http://localhost:1455/auth/callback?code=...&state=..." /></div>
          <p v-if="reauthError" role="alert" class="text-sm leading-6 text-red-600 dark:text-red-400">{{ reauthError }}</p>
        </div>
        <template #footer><button class="btn btn-secondary" :disabled="busy" @click="closeReauthorization">取消</button><button class="btn btn-primary" :disabled="busy || !reauthStart || !reauthCallbackURL.trim()" @click="completeReauthorization">{{ busy ? '正在处理…' : '提交回调并重新授权' }}</button></template>
      </BaseDialog>
      <ConfirmDialog :show="!!confirmation" :title="confirmation?.title || ''" :message="confirmation?.message || ''" :danger="confirmation?.action === 'delete'" @cancel="confirmation = null" @confirm="confirmAccountAction" />
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { buildGatewayUrl } from '@/api/client'
import {
  completeOpenAIOnboarding,
  getOnboardingOverview,
  startOpenAIOnboarding,
  setOnboardingAccountPaused,
  deleteOnboardingAccount,
  startOnboardingAccountReauthorization,
  completeOnboardingAccountReauthorization,
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
import BaseDialog from '@/components/common/BaseDialog.vue'
import ServiceAccountDetails from '@/components/common/ServiceAccountDetails.vue'
import { useAppStore } from '@/stores/app'

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
const reauthAccount = ref<OnboardingAccountSummary | null>(null)
const reauthStart = ref<OnboardingOAuthStart | null>(null)
const reauthCallbackURL = ref('')
const reauthError = ref('')
const accountColumns = [{ key: 'name', label: 'GPT 账号' }, { key: 'plan_type', label: '套餐' }, { key: 'status', label: '状态' }, { key: 'actions', label: '操作' }]
const confirmation = ref<{ account: OnboardingAccountSummary; action: 'pause' | 'resume' | 'delete'; title: string; message: string } | null>(null)
const reportError = (error: unknown) => app.showError((error as { response?: { data?: { message?: string } } })?.response?.data?.message || (error instanceof TypeError ? '回调链接格式无效，请粘贴完整的回调地址' : '操作失败，请稍后重试'))
function openReauthorization(account: OnboardingAccountSummary) {
  if (busy.value) return
  reauthStart.value = null
  reauthCallbackURL.value = ''
  reauthError.value = ''
  reauthAccount.value = account
}
function closeReauthorization() {
  if (!busy.value) reauthAccount.value = null
}
async function startReauthorization() {
  if (busy.value || !reauthAccount.value) return
  busy.value = true
  reauthError.value = ''
  reauthStart.value = null
  reauthCallbackURL.value = ''
  const popup = window.open('about:blank', '_blank')
  try {
    reauthStart.value = await startOnboardingAccountReauthorization(reauthAccount.value.id)
    if (popup) { popup.opener = null; popup.location.href = reauthStart.value.auth_url }
  } catch (error) {
    popup?.close()
    reauthError.value = (error as { response?: { data?: { message?: string } } })?.response?.data?.message || '无法打开授权，请稍后重试'
  } finally { busy.value = false }
}
async function completeReauthorization() {
  const account = reauthAccount.value
  const session = reauthStart.value
  if (busy.value || !account || !session) return
  busy.value = true
  reauthError.value = ''
  try {
    const parsed = new URL(reauthCallbackURL.value.trim())
    const code = parsed.searchParams.get('code')
    const state = parsed.searchParams.get('state')
    if (!code || !state) throw new TypeError('invalid callback')
    await completeOnboardingAccountReauthorization(account.id, { session_id: session.session_id, code, state, onboarding_state: session.onboarding_state })
    reauthAccount.value = null
    app.showSuccess(`“${account.name}”${account.email ? `（${account.email}）` : ''}重新授权成功`)
    await load()
  } catch (error) {
    reauthError.value = (error as { response?: { data?: { message?: string } } })?.response?.data?.message || (error instanceof TypeError ? '回调链接格式无效，请粘贴包含 code 和 state 的完整链接' : '重新授权失败，请重新开始授权')
  } finally { busy.value = false }
}
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
})
</script>
