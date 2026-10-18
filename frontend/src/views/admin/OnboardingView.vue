<template>
    <div class="w-full space-y-8">
      <header class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">新手引导管理</h1>
        <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">配置 Discord 授权、OpenAI OAuth、套餐换算和 CC Switch 下载地址。</p>
      </header>

      <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex items-center justify-between gap-4">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">基础配置</h2>
          <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存配置' }}</button>
        </div>
        <div class="mt-5 grid gap-4 md:grid-cols-2">
          <label class="flex items-center gap-3 text-sm text-gray-700 dark:text-gray-200"><input v-model="form.enabled" type="checkbox" />启用新手引导</label>
          <label class="flex items-center gap-3 text-sm text-gray-700 dark:text-gray-200"><input v-model="form.public_fallback_default" type="checkbox" />新账号默认允许公共回退</label>
          <label class="space-y-1 text-sm"><span>Discord Client ID</span><input v-model="form.discord.client_id" class="form-input w-full" /></label>
          <label class="space-y-1 text-sm"><span>Discord Client Secret（留空保持不变）</span><input v-model="form.discord.client_secret" type="password" class="form-input w-full" /></label>
          <label class="space-y-1 text-sm"><span>Discord Bot Token（留空保持不变）</span><input v-model="form.discord.bot_token" type="password" class="form-input w-full" /></label>
          <label class="space-y-1 text-sm"><span>服务器 ID</span><input v-model="form.discord.guild_id" class="form-input w-full" /></label>
          <label class="space-y-1 text-sm"><span>身份组 ID</span><input v-model="form.discord.role_id" class="form-input w-full" /></label>
          <label class="space-y-1 text-sm"><span>Discord 回调地址</span><input v-model="form.discord.redirect_uri" class="form-input w-full" placeholder="https://example.com/onboarding" /></label>
          <label class="space-y-1 text-sm"><span>OpenAI 回调地址</span><input v-model="form.openai_redirect_uri" class="form-input w-full" placeholder="https://example.com/onboarding" /></label>
          <label class="space-y-1 text-sm md:col-span-2"><span>CC Switch 下载地址</span><input v-model="form.ccswitch_download_url" class="form-input w-full" /></label>
        </div>
      </section>

      <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">绑定已有 OpenAI OAuth 账号</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">账号 ID 可在“账号管理”中查看；绑定后会重新计算该用户的 Plus 等值和订阅。</p>
        <div class="mt-4 flex flex-wrap items-end gap-3">
          <label class="space-y-1 text-sm"><span>未分配账号</span><select v-model.number="bindForm.account_id" class="form-input w-64"><option :value="0">请选择账号</option><option v-for="account in overview?.unassigned_accounts || []" :key="account.id" :value="account.id">{{ account.name }} · {{ account.plan_type || '未知套餐' }}（#{{ account.id }}）</option></select></label>
          <label class="space-y-1 text-sm"><span>账号 ID</span><input v-model.number="bindForm.account_id" type="number" min="1" class="form-input w-32" /></label>
          <label class="space-y-1 text-sm"><span>用户 ID</span><input v-model.number="bindForm.user_id" type="number" min="1" class="form-input w-32" /></label>
          <label class="flex items-center gap-2 pb-2 text-sm"><input v-model="bindForm.public_fallback" type="checkbox" />允许公共回退</label>
          <button class="btn btn-primary" :disabled="!bindForm.account_id || !bindForm.user_id" @click="bind">绑定账号</button>
        </div>
      </section>

      <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">套餐换算和订阅分配</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">单位以 Plus 等值计算：Plus/Business Standard 通常为 1，Pro/Business Premium 通常为 5。</p>
        <div class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <label v-for="plan in planNames" :key="plan" class="space-y-1 text-sm"><span>{{ plan }} 单位</span><input v-model.number="form.plan_units[plan]" type="number" min="0" class="form-input w-full" /></label>
        </div>
        <div class="mt-5 overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
          <table class="min-w-full text-left text-sm"><thead class="bg-gray-50 dark:bg-dark-700/50"><tr><th class="px-4 py-3">套餐</th><th class="px-4 py-3">分组 ID</th><th class="px-4 py-3">有效期（天）</th><th class="px-4 py-3">消耗单位</th></tr></thead>
            <tbody><tr v-for="plan in planNames" :key="plan" class="border-t border-gray-100 dark:border-dark-700"><td class="px-4 py-3">{{ plan }}</td><td class="px-4 py-3"><input v-model.number="form.subscription_rules[plan].group_id" type="number" min="0" class="form-input w-32" /></td><td class="px-4 py-3"><input v-model.number="form.subscription_rules[plan].validity_days" type="number" min="1" class="form-input w-28" /></td><td class="px-4 py-3"><input v-model.number="form.subscription_rules[plan].units" type="number" min="0" class="form-input w-24" /></td></tr></tbody>
          </table>
        </div>
      </section>

      <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex items-center justify-between gap-4"><h2 class="text-lg font-semibold text-gray-900 dark:text-white">用户和账号归属</h2><button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button></div>
        <div v-if="loading" class="py-8 text-center text-sm text-gray-500">正在加载…</div>
        <div v-else class="mt-4 space-y-3">
          <div v-for="user in overview?.users || []" :key="user.user_id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
            <div class="flex flex-wrap items-center justify-between gap-3"><div><p class="font-medium text-gray-900 dark:text-white">{{ user.username || '未设置用户名' }}</p><p class="text-sm text-gray-500">{{ user.email }} · {{ user.account_units }} 个 Plus 等值</p></div><span class="text-sm text-gray-500">{{ user.discord?.status === 'active' ? `Discord：${user.discord.discord_username}` : '未绑定 Discord' }}</span></div>
            <div v-if="user.accounts.length" class="mt-3 grid gap-2 sm:grid-cols-2"><div v-for="account in user.accounts" :key="account.id" class="flex items-center justify-between rounded-lg bg-gray-50 px-3 py-2 text-sm dark:bg-dark-700/50"><span>{{ account.name }} · {{ account.plan_type || '未知套餐' }}</span><button class="text-red-600 hover:underline" @click="unbind(account.id)">解绑</button></div></div>
            <p v-else class="mt-3 text-sm text-gray-500">暂无专属账号，请在账号管理中复制账号 ID 后使用下方绑定。</p>
          </div>
        </div>
      </section>
    </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { onboardingAPI } from '@/api/admin/onboarding'
import type { OnboardingAdminOverview } from '@/api/admin/onboardingTypes'
import type { OnboardingConfig } from '@/api/user'


const planNames = ['plus', 'pro', 'business_premium', 'business_standard'] as const
type PlanName = (typeof planNames)[number]
type Rule = { group_id: number; validity_days: number; units: number }

function emptyRule(): Rule {
  return { group_id: 0, validity_days: 30, units: 0 }
}

const overview = ref<OnboardingAdminOverview | null>(null)
const loading = ref(false)
const saving = ref(false)
const bindForm = reactive({ account_id: 0, user_id: 0, public_fallback: false })
const form = reactive({
  enabled: true,
  public_fallback_default: false,
  discord: { client_id: '', client_secret: '', bot_token: '', guild_id: '', role_id: '', redirect_uri: '' },
  openai_redirect_uri: '',
  ccswitch_download_url: '',
  plan_units: { plus: 1, pro: 5, business_premium: 5, business_standard: 1 } as Record<PlanName, number>,
  subscription_rules: { plus: emptyRule(), pro: emptyRule(), business_premium: emptyRule(), business_standard: emptyRule() } as Record<PlanName, Rule>,
})

function applyConfig(config: OnboardingConfig) {
  form.enabled = config.enabled
  form.public_fallback_default = config.public_fallback_default
  form.discord.client_id = config.discord.client_id || ''
  form.discord.guild_id = config.discord.guild_id || ''
  form.discord.role_id = config.discord.role_id || ''
  form.discord.redirect_uri = config.discord.redirect_uri || ''
  form.openai_redirect_uri = config.openai_redirect_uri || ''
  form.ccswitch_download_url = config.ccswitch_download_url || ''
  for (const plan of planNames) {
    form.plan_units[plan] = config.plan_units?.[plan] ?? form.plan_units[plan]
    form.subscription_rules[plan] = { ...emptyRule(), ...(config.subscription_rules?.[plan] || {}) }
  }
}

async function load() {
  loading.value = true
  try {
    overview.value = await onboardingAPI.getOverview()
    applyConfig(overview.value.config)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const config = await onboardingAPI.updateConfig({
      enabled: form.enabled,
      public_fallback_default: form.public_fallback_default,
      discord: form.discord,
      openai_redirect_uri: form.openai_redirect_uri,
      ccswitch_download_url: form.ccswitch_download_url,
      plan_units: form.plan_units,
      subscription_rules: form.subscription_rules,
    })
    applyConfig(config)
    if (overview.value) overview.value.config = config
  } finally {
    saving.value = false
  }
}

async function unbind(accountID: number) {
  if (!window.confirm('确定解绑这个账号吗？解绑后将停止为对应用户分配订阅。')) return
  await onboardingAPI.unbindAccount(accountID)
  await load()
}

async function bind() {
  await onboardingAPI.bindAccount(bindForm.account_id, bindForm.user_id, bindForm.public_fallback)
  bindForm.account_id = 0
  await load()
}

onMounted(load)
</script>
