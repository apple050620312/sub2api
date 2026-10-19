<template>
  <div class="space-y-8">
    <section class="card overflow-visible">
      <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-100 p-6 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold">用户与账号</h2><p class="mt-1 text-sm text-gray-500">账号优先服务所属用户，忙碌时自动回退公共池。</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button>
      </div>
      <div class="p-6"><input v-model="search" class="input w-full sm:max-w-md" aria-label="搜索用户或账号" placeholder="搜索用户邮箱、Discord 或 GPT 账号名称" /></div>
      <DataTable :columns="userColumns" :data="filteredUsers" :loading="loading" row-key="user_id" :actions-count="2">
        <template #cell-email="{ row }"><p class="font-medium">{{ row.email }}</p><p class="mt-1 text-xs text-gray-500">{{ row.username || '未设置用户名' }}</p></template>
        <template #cell-discord="{ row }">{{ row.discord?.discord_username || '未绑定' }}</template>
        <template #cell-accounts="{ row }"><div v-if="row.accounts.length" class="space-y-3"><div v-for="account in row.accounts" :key="account.id" class="flex flex-wrap items-center gap-3"><button class="text-left text-primary-600 hover:underline" @click="detail = account"><span class="block">{{ account.name }}</span><span class="mt-1 block text-xs text-gray-500">{{ account.email || '未提供邮箱' }} · {{ serviceAccountPlanLabel(account.plan_type) }}</span></button><button class="btn btn-secondary btn-sm" :disabled="busy" :aria-label="`解绑 ${row.email} 的 ${account.name}`" @click="askUnbind(row, account)">解绑</button></div></div><span v-else class="text-gray-400">暂无账号</span></template>
        <template #cell-account_units="{ row }"><p class="whitespace-nowrap">{{ row.account_units }} 个 Plus 等值</p><p class="mt-1 text-xs text-gray-500">{{ assignedGroupName(row.account_units) }}</p></template>
        <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" @click="openAssignment(row, 'account')">分配账号</button><button class="btn btn-secondary btn-sm" @click="openAssignment(row, 'discord')">绑定 Discord</button></div></template>
      </DataTable>
    </section>
    <section class="card p-6">
      <div class="mb-6 flex flex-wrap items-start justify-between gap-4"><div><h2 class="text-lg font-semibold">服务配置</h2><p class="mt-1 text-sm text-gray-500">配置 Discord、新手接入流程和自动订阅。保存后会重新核对账号资格。</p></div><button class="btn btn-primary" :disabled="busy || loading" @click="save">{{ busy ? '处理中…' : '保存配置' }}</button></div>
      <label class="mb-6 flex items-center gap-3 text-sm"><input v-model="form.enabled" type="checkbox" class="rounded" />启用新手引导</label>
      <div class="grid gap-5 md:grid-cols-2">
        <label class="space-y-2 text-sm"><span>Discord Client ID</span><input v-model="form.discord.client_id" class="input" /></label>
        <label class="space-y-2 text-sm"><span>Discord Client Secret</span><input v-model="form.discord.client_secret" type="password" autocomplete="new-password" class="input" :placeholder="form.discord.client_secret_configured ? '已配置，留空保持不变' : '请输入 Client Secret'" /></label>
        <label class="space-y-2 text-sm"><span>Discord Bot Token</span><input v-model="form.discord.bot_token" type="password" autocomplete="new-password" class="input" :placeholder="form.discord.bot_token_configured ? '已配置，留空保持不变' : '请输入 Bot Token'" /></label>
        <label class="space-y-2 text-sm"><span>Discord 服务器 ID</span><input v-model="form.discord.guild_id" class="input" /></label>
        <label class="space-y-2 text-sm"><span>Discord 身份组 ID</span><input v-model="form.discord.role_id" class="input" /></label>
        <label class="space-y-2 text-sm"><span>Discord 回调地址</span><input v-model="form.discord.redirect_uri" class="input" placeholder="https://example.com/onboarding" /></label>
        <label class="space-y-2 text-sm"><span>OpenAI 回调地址</span><input v-model="form.openai_redirect_uri" class="input" placeholder="留空使用默认 OAuth 流程" /></label>
        <label class="space-y-2 text-sm"><span>CC Switch 下载地址</span><input v-model="form.ccswitch_download_url" class="input" /></label>
      </div>
      <div class="mb-5 mt-8"><h3 class="font-semibold">按账号数量分配订阅</h3><p class="mt-2 text-sm leading-6 text-gray-500">把所有有效账号换算成 Plus 等值，每人只匹配一个档位。1～4 个分别对应下表；5 个及以上使用最后一档。未配置的档位不自动分配订阅。</p></div>
      <p v-if="overview?.config.subscription_rules_need_review" class="mb-5 rounded-lg bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200" role="alert">旧规则中存在同一数量对应多个不同订阅的情况，相关档位已留空。请按下面的账号数量重新选择并保存。</p>
      <div class="mb-5 rounded-lg bg-gray-50 p-4 text-sm leading-6 text-gray-600 dark:bg-dark-900 dark:text-gray-300">Plus、Business Standard = 1 个等值；Pro 5x、Business Premium = 5 个等值。普通 Business（team）按 Standard 识别；Pro 20x、Pro 50x 也使用 5 个及以上档位。</div>
      <DataTable :columns="tierColumns" :data="tiers" row-key="key">
        <template #cell-label="{ row }"><p class="whitespace-nowrap font-medium">{{ row.label }}</p><p class="mt-1 text-xs text-gray-500">{{ row.example }}</p></template>
        <template #cell-group="{ row }"><div class="min-w-64"><Select v-model="form.subscription_rules[row.key]!.group_id" :options="groupOptions" searchable :aria-label="`${row.label} 对应订阅`" /></div></template>
        <template #cell-validity="{ row }"><input v-model.number="form.subscription_rules[row.key]!.validity_days" type="number" min="1" max="36500" class="input w-28" :aria-label="`${row.label} 订阅有效期（天）`" /></template>
      </DataTable>
      <p class="mt-5 text-sm leading-6 text-gray-500">例如 2 支 Plus 会获得“2 个 Plus 等值”这一行选择的订阅，不会叠加 1 个等值的订阅。暂停、删除或解绑账号后重新匹配档位；恢复原档位保留订阅原有效期及用量。手动购买的订阅不受影响。</p>
    </section>
    <BaseDialog :show="!!assignment" :title="assignmentKind === 'account' ? '分配 GPT 账号' : '绑定现有 Discord 成员'" width="normal" @close="assignment = null">
      <div class="space-y-5"><p class="rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-900">目标用户：<strong class="break-all">{{ assignment?.email }}</strong></p>
        <template v-if="assignmentKind === 'account'"><label class="mb-2 block text-sm">选择 GPT 账号</label><Select v-model="accountID" :options="accountOptions" searchable aria-label="选择 GPT 账号" placeholder="按账号名称或邮箱选择" /></template>
        <template v-else><p class="text-sm text-gray-500">搜索服务器里的昵称或 Discord 用户名。机器人需具有成员列表权限和授予身份组权限。</p><Select v-model="memberID" :options="memberOptions" remote :loading="membersLoading" aria-label="选择 Discord 成员" placeholder="搜索 Discord 成员" @search="loadMembers" /><p v-if="memberError" role="alert" class="text-sm text-red-600">{{ memberError }}</p></template>
      </div>
      <template #footer><button class="btn btn-secondary" @click="assignment = null">取消</button><button class="btn btn-primary ml-3" :disabled="busy || (assignmentKind === 'account' ? !accountID : !memberID)" @click="askAssignment">下一步</button></template>
    </BaseDialog>
    <ServiceAccountDetails :account="detail" @close="detail = null" />
    <ConfirmDialog :show="!!confirmation" :title="confirmation?.title || ''" :message="confirmation?.message || ''" :danger="confirmation?.danger" @cancel="confirmation = null" @confirm="confirm" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ServiceAccountDetails from '@/components/common/ServiceAccountDetails.vue'
import { onboardingAPI } from '@/api/admin/onboarding'
import type { OnboardingAdminOverview, OnboardingAdminUser, OnboardingDiscordMember } from '@/api/admin/onboardingTypes'
import type { OnboardingAccountSummary, OnboardingConfig } from '@/api/user'
import { getAll } from '@/api/admin/groups'
import { useAppStore } from '@/stores/app'
import { serviceAccountPlanLabel } from '@/utils/serviceCenter'

const app = useAppStore()
const tiers = Array.from({ length: 5 }, (_, index) => ({ key: String(index + 1), label: index === 4 ? '5 个及以上 Plus 等值' : `${index + 1} 个 Plus 等值`, example: index === 4 ? '5 支 Plus / 1 支 Pro 5x / 1 支 Business Premium' : `${index + 1} 支 Plus 或 Business Standard` }))
const tierColumns = [{ key: 'label', label: '有效账号数量' }, { key: 'group', label: '对应订阅' }, { key: 'validity', label: '有效期（天）' }]
const overview = ref<OnboardingAdminOverview | null>(null)
const loading = ref(false)
const busy = ref(false)
const search = ref('')
const groupOptions = ref<Array<{ value: number; label: string }>>([{ value: 0, label: '不自动分配订阅' }])
const emptyRules = () => Object.fromEntries(tiers.map(tier => [tier.key, { group_id: 0, validity_days: 30 }]))
const form = reactive<OnboardingConfig & { discord: OnboardingConfig['discord'] & { client_secret: string; bot_token: string } }>({ enabled: true, discord: { client_id: '', client_secret: '', bot_token: '', client_secret_configured: false, bot_token_configured: false, guild_id: '', role_id: '', redirect_uri: '' }, subscription_rules: emptyRules(), openai_redirect_uri: '', ccswitch_download_url: '' })
const userColumns = [{ key: 'email', label: '用户' }, { key: 'discord', label: 'Discord' }, { key: 'accounts', label: 'GPT 账号' }, { key: 'account_units', label: '有效贡献' }, { key: 'actions', label: '操作' }]
const filteredUsers = computed(() => (overview.value?.users || []).filter(user => `${user.email} ${user.username} ${user.discord?.discord_username || ''} ${user.accounts.map(account => `${account.name} ${account.email}`).join(' ')}`.toLowerCase().includes(search.value.toLowerCase())))
const detail = ref<OnboardingAccountSummary | null>(null)
const assignment = ref<OnboardingAdminUser | null>(null)
const assignmentKind = ref<'account' | 'discord'>('account')
const accountID = ref<number>(0)
const memberID = ref('')
const members = ref<OnboardingDiscordMember[]>([])
const membersLoading = ref(false)
const memberError = ref('')
let memberRequest = 0
const accountOptions = computed(() => (overview.value?.unassigned_accounts || []).map(account => ({ value: account.id, label: `${account.name} · ${account.email || serviceAccountPlanLabel(account.plan_type)}` })))
const memberOptions = computed(() => members.value.map(member => ({ value: member.id, label: `${member.display_name} (@${member.username})${member.bound_email ? ` · 已绑定 ${member.bound_email}` : ''}`, disabled: !!member.bound_email && member.bound_email !== assignment.value?.email })))
const confirmation = ref<{ title: string; message: string; danger?: boolean; action: () => Promise<void> } | null>(null)
const errorMessage = (error: unknown) => (error as { response?: { data?: { message?: string } } })?.response?.data?.message || '操作失败，请稍后重试'
async function load() {
  loading.value = true
  try {
    const [data, groups] = await Promise.all([onboardingAPI.getOverview(), getAll('openai')])
    overview.value = data
    const rules = { ...emptyRules(), ...structuredClone(data.config.subscription_rules) }
    Object.assign(form, data.config, { discord: { ...data.config.discord, client_secret: '', bot_token: '' }, subscription_rules: rules })
    groupOptions.value = [{ value: 0, label: '不自动分配订阅' }, ...groups.filter(group => group.subscription_type === 'subscription' && group.status === 'active').map(group => ({ value: group.id, label: group.name }))]
  } catch (error) { app.showError(errorMessage(error)) } finally { loading.value = false }
}
async function perform(action: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true
  try { await action(); app.showSuccess('操作成功'); await load() } catch (error) { app.showError(errorMessage(error)) } finally { busy.value = false }
}
function assignedGroupName(units: number) {
  const rule = overview.value?.config.subscription_rules[String(Math.min(5, units))]
  return groupOptions.value.find(group => group.value === rule?.group_id)?.label || '未配置对应订阅'
}
function save() {
  const payload = { ...form, discord: { ...form.discord }, subscription_rules: Object.fromEntries(Object.entries(form.subscription_rules).map(([tier, rule]) => [tier, { ...rule }])) }
  const mapping = tiers.map(tier => `${tier.label}：${groupOptions.value.find(group => group.value === form.subscription_rules[tier.key]?.group_id)?.label || '不自动分配订阅'}`).join('；')
  confirmation.value = { title: '确认保存服务配置', message: `${mapping}。保存后将重新匹配所有用户的自动订阅，手动订阅保持不变。`, action: async () => { await onboardingAPI.updateConfig(payload) } }
}
async function loadMembers(query = '') {
  const request = ++memberRequest
  membersLoading.value = true
  memberError.value = ''
  try { const result = await onboardingAPI.getDiscordMembers(query); if (request === memberRequest) members.value = result } catch (error) { if (request === memberRequest) memberError.value = errorMessage(error) } finally { if (request === memberRequest) membersLoading.value = false }
}
function openAssignment(user: OnboardingAdminUser, kind: 'account' | 'discord') {
  assignment.value = user
  assignmentKind.value = kind
  accountID.value = 0
  memberID.value = ''
  if (kind === 'discord') void loadMembers()
}
function askAssignment() {
  const user = assignment.value
  if (!user) return
  const account = overview.value?.unassigned_accounts.find(account => account.id === accountID.value)
  const member = members.value.find(member => member.id === memberID.value)
  if (assignmentKind.value === 'account' && account) {
    confirmation.value = { title: '确认分配账号', message: `将 GPT 账号“${account.name}”（${account.email || serviceAccountPlanLabel(account.plan_type)}）绑定到 ${user.email}，并更新其订阅资格。`, action: async () => { await onboardingAPI.bindAccount(account.id, user.user_id); assignment.value = null } }
  } else if (assignmentKind.value === 'discord' && member) {
    confirmation.value = { title: '确认绑定 Discord', message: `将 Discord 成员“${member.display_name}”（@${member.username}）绑定到 ${user.email}，并授予配置的身份组。`, action: async () => { await onboardingAPI.bindDiscordMember(user.user_id, member.id); assignment.value = null } }
  }
}
function askUnbind(user: OnboardingAdminUser, account: OnboardingAccountSummary) {
  confirmation.value = { title: '确认解绑账号', message: `解除 ${user.email} 与 GPT 账号“${account.name}”的绑定。该用户的自动订阅资格将重新计算。`, danger: true, action: () => onboardingAPI.unbindAccount(account.id) }
}
async function confirm() { const pending = confirmation.value; if (!pending || busy.value) return; confirmation.value = null; await perform(pending.action) }
onMounted(load)
</script>
