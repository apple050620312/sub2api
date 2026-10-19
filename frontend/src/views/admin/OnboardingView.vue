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
      <div class="mt-8 space-y-3">
        <h3 class="font-semibold">新账号调用规则</h3>
        <label class="block text-sm" for="service-account-template">指定模板账号</label>
        <div class="max-w-2xl"><Select id="service-account-template" v-model="form.account_template_id" :options="templateOptions" searchable aria-label="指定模板账号" /></div>
        <p class="text-sm leading-6 text-gray-500">新接入的 OAuth 账号复制此模板的模型规则、分组、代理、并发、优先级及计费倍率。账号凭据、身份、用量和临时状态使用新账号自己的数据。修改模板不会覆盖已接入账号。</p>
        <p v-if="!form.account_template_id" class="text-sm text-amber-600" role="alert">尚未指定模板，新用户暂时无法添加账号。</p>
        <p v-else-if="!templateOptions.some(option => option.value === form.account_template_id)" class="text-sm text-amber-600" role="alert">之前指定的模板已不可用，请重新选择。</p>
      </div>
      <div class="mt-8 space-y-5 border-t border-gray-100 pt-8 dark:border-dark-700">
        <div><h3 class="font-semibold">全池重置联动</h3><p class="mt-2 text-sm leading-6 text-gray-500">检测全池异常重置或同批新重置券，确认后恢复所有用户的订阅配额，并发送你设置的 Discord 通知。</p></div>
        <label class="flex items-center gap-3 text-sm"><input v-model="form.pool_reset.enabled" type="checkbox" class="rounded" />启用自动检测与全员配额重置</label>
        <div class="grid gap-5 md:grid-cols-2">
          <div class="space-y-2 text-sm"><label>Discord 通知频道</label><div class="flex items-start gap-3"><div class="min-w-0 flex-1"><Select v-model="form.pool_reset.channel_id" :options="channelOptions" searchable aria-label="Discord 通知频道" placeholder="选择文字频道" /></div><button class="btn btn-secondary" :disabled="channelsLoading" @click="loadChannels">{{ channelsLoading ? '加载中…' : '刷新频道' }}</button></div><p class="text-xs leading-5 text-gray-500">先保存机器人和服务器配置，再选择频道。机器人需要查看频道和发送消息权限。</p><p v-if="channelError" class="text-sm text-red-600" role="alert">{{ channelError }}</p></div>
          <label class="space-y-2 text-sm"><span>重置后发送的消息</span><textarea v-model="form.pool_reset.message" rows="4" maxlength="2000" class="input" placeholder="输入需要机器人发送的通知内容" /><span class="block text-xs text-gray-500">原样发送，最多 2000 字，不触发 @ 提及。</span></label>
        </div>
        <div class="rounded-lg bg-gray-50 p-4 text-sm leading-6 text-gray-600 dark:bg-dark-900 dark:text-gray-300">每两分钟启动一轮查询。至少 3 个独立 GPT 账号、90% 以上出现相同证据，并在下一轮再次确认才会执行；首次启用或账号池变化先建立基线。自然到期、单账号用券、数据缺失及过期快照不会触发。重置覆盖有效订阅的日、周、月用量和滚动五小时用量，不延长订阅有效期、不删除使用记录。</div>
        <div v-if="overview?.pool_reset_status" class="space-y-3 rounded-lg border border-gray-200 p-4 text-sm dark:border-dark-700" aria-label="全池重置检测状态">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3"><StatusBadge :status="overview.config.pool_reset?.enabled ? 'active' : 'inactive'" :label="overview.config.pool_reset?.enabled ? resetPhase : '未启用'" /><span>本轮有效快照：{{ overview.pool_reset_status.fresh_accounts }} / {{ overview.pool_reset_status.total_accounts }}</span><span>待确认账号：{{ overview.pool_reset_status.pending_accounts }}</span></div>
          <p class="text-gray-500">最近检查：{{ displayTime(overview.pool_reset_status.checked_at) }}</p>
          <p v-if="overview.pool_reset_status.last_reset_at">上次重置：{{ displayTime(overview.pool_reset_status.last_reset_at) }} · {{ overview.pool_reset_status.last_reset_kind === 'credits' ? '同批新重置券' : '五小时和七天窗口共同重置' }} · {{ overview.pool_reset_status.affected_subscriptions }} 份订阅</p>
          <p v-if="overview.pool_reset_status.notification_state">Discord 通知：{{ notificationLabel }}</p>
        </div>
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
import StatusBadge from '@/components/common/StatusBadge.vue'
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
const emptyPoolReset = () => ({ enabled: false, channel_id: '', message: '' })
const form = reactive<OnboardingConfig & { account_template_id: number; pool_reset: ReturnType<typeof emptyPoolReset>; discord: OnboardingConfig['discord'] & { client_secret: string; bot_token: string } }>({ enabled: true, account_template_id: 0, pool_reset: emptyPoolReset(), discord: { client_id: '', client_secret: '', bot_token: '', client_secret_configured: false, bot_token_configured: false, guild_id: '', role_id: '', redirect_uri: '' }, subscription_rules: emptyRules(), openai_redirect_uri: '', ccswitch_download_url: '' })
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
const templateOptions = computed(() => [{ value: 0, label: '暂不接入新账号' }, ...(overview.value?.template_accounts || []).map(account => ({ value: account.id, label: `${account.name} · ${account.email || serviceAccountPlanLabel(account.plan_type)}` }))])
const channels = ref<Array<{ id: string; name: string }>>([])
const channelsLoading = ref(false)
const channelError = ref('')
const channelOptions = computed(() => channels.value.map(channel => ({ value: channel.id, label: `#${channel.name}` })))
const resetPhase = computed(() => ({ warming_up: '正在建立基线', insufficient_accounts: '独立账号不足 3 个', monitoring: '监测中', incomplete: '有效快照不足，暂不重置', confirming: '等待下一轮确认' })[overview.value?.pool_reset_status?.phase || ''] || '等待首次检查')
const notificationLabel = computed(() => ({ sent: '已发送', pending: '等待发送', sending: '已开始发送；若长时间不变，请核对频道', failed: '发送失败，请核对机器人和频道权限', uncertain: '发送结果待核对；为避免重复通知，不自动重发' })[overview.value?.pool_reset_status?.notification_state || ''] || '暂无通知')
const displayTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未检查'
const memberOptions = computed(() => members.value.map(member => ({ value: member.id, label: `${member.display_name} (@${member.username})${member.bound_email ? ` · 已绑定 ${member.bound_email}` : ''}`, disabled: !!member.bound_email && member.bound_email !== assignment.value?.email })))
const confirmation = ref<{ title: string; message: string; danger?: boolean; action: () => Promise<void> } | null>(null)
const errorMessage = (error: unknown) => (error as { response?: { data?: { message?: string } } })?.response?.data?.message || '操作失败，请稍后重试'
async function load() {
  loading.value = true
  try {
    const [data, groups] = await Promise.all([onboardingAPI.getOverview(), getAll('openai')])
    overview.value = data
    const rules = { ...emptyRules(), ...structuredClone(data.config.subscription_rules) }
    Object.assign(form, data.config, { account_template_id: data.config.account_template_id || 0, pool_reset: { ...emptyPoolReset(), ...data.config.pool_reset }, discord: { ...data.config.discord, client_secret: '', bot_token: '' }, subscription_rules: rules })
    if (form.pool_reset.channel_id) void loadChannels()
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
  const payload = { ...form, pool_reset: { ...form.pool_reset }, discord: { ...form.discord }, subscription_rules: Object.fromEntries(Object.entries(form.subscription_rules).map(([tier, rule]) => [tier, { ...rule }])) }
  const mapping = tiers.map(tier => `${tier.label}：${groupOptions.value.find(group => group.value === form.subscription_rules[tier.key]?.group_id)?.label || '不自动分配订阅'}`).join('；')
  const template = templateOptions.value.find(option => option.value === form.account_template_id)?.label || '模板已不可用'
  const reset = form.pool_reset.enabled ? `启用全池重置联动，确认事件后将重置所有用户的有效订阅配额，通知发送到 ${channelOptions.value.find(option => option.value === form.pool_reset.channel_id)?.label || '所选频道'}。` : '全池重置联动保持关闭。'
  confirmation.value = { title: '确认保存服务配置', message: `新账号模板：${template}。${reset}${mapping}。保存后将重新匹配所有用户的自动订阅，手动订阅保持不变。`, action: async () => { await onboardingAPI.updateConfig(payload) } }
}
async function loadChannels() {
  channelsLoading.value = true
  channelError.value = ''
  try { channels.value = await onboardingAPI.getDiscordChannels() } catch (error) { channelError.value = errorMessage(error) } finally { channelsLoading.value = false }
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
