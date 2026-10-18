<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-7xl space-y-6">
      <header class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <p class="text-sm font-semibold text-primary-600 dark:text-primary-400">新用户引导</p>
        <h1 class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">从绑定 Discord 到开始使用 Codex</h1>
        <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">按顺序完成下面四步。你的邮箱和 OAuth 令牌不会显示给其他用户。</p>
      </header>

      <div v-if="loading" class="rounded-2xl border border-gray-200 bg-white p-8 text-center text-gray-500 dark:border-dark-700 dark:bg-dark-800">正在加载引导状态…</div>
      <template v-else>
        <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第一步</p>
              <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">绑定 Discord</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">授权后自动加入指定服务器并获得身份组。</p>
            </div>
            <span class="rounded-full px-3 py-1 text-sm font-semibold" :class="overview?.discord?.status === 'active' ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'">
              {{ overview?.discord?.status === 'active' ? '已绑定' : '未完成' }}
            </span>
          </div>
          <p v-if="overview?.discord" class="mt-4 text-sm text-gray-600 dark:text-gray-300">Discord：{{ overview.discord.discord_username }}</p>
          <button class="btn btn-primary mt-4" :disabled="busy" @click="startDiscord">{{ overview?.discord ? '重新绑定 Discord' : '绑定 Discord' }}</button>
        </section>

        <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第二步</p>
              <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">添加自己的 OpenAI OAuth 账号</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">只允许 Plus、Pro 或 Business 账号，账号会优先服务你自己。</p>
            </div>
            <span class="rounded-full px-3 py-1 text-sm font-semibold" :class="overview?.accounts.length ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'">{{ overview?.accounts.length ? `${overview.account_units} 个 Plus 等值` : '未添加' }}</span>
          </div>
          <div class="mt-4 flex flex-wrap gap-3">
            <button class="btn btn-primary" :disabled="busy || !overview?.ready_for_account" @click="startOpenAI">打开 OpenAI 授权</button>
            <span v-if="!overview?.ready_for_account" class="self-center text-sm text-amber-600">请先完成 Discord 绑定。</span>
          </div>
          <div v-if="oauthStart" class="mt-5 space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700/50">
            <p class="text-sm text-gray-700 dark:text-gray-200">授权完成后，把回调地址粘贴到下面，系统会自动提取 code 和 state。</p>
            <a class="break-all text-sm text-primary-600 hover:underline" :href="oauthStart.auth_url" target="_blank" rel="noreferrer">重新打开授权页面</a>
            <textarea v-model="callbackURL" class="form-input min-h-20 w-full" placeholder="粘贴 http://localhost:1455/auth/callback?code=...&state=..."></textarea>
            <button class="btn btn-primary" :disabled="busy || !callbackURL || !oauthSessionID" @click="completeOpenAI">提交回调并绑定账号</button>
          </div>
          <div v-if="overview?.accounts.length" class="mt-5 grid gap-3 sm:grid-cols-2">
            <div v-for="account in overview.accounts" :key="account.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
              <p class="font-medium text-gray-900 dark:text-white">{{ account.name }}</p>
              <p class="mt-1 text-sm text-gray-500">{{ account.plan_type || '未知套餐' }} · {{ account.status }}</p>
            </div>
          </div>
        </section>

        <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第三步</p>
          <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">创建 API Key</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">使用这个 Key 连接 CC Switch。完整 Key 只会在创建成功后显示一次。</p>
          <div class="mt-4 flex flex-wrap gap-3">
            <input v-model="keyName" class="form-input w-full sm:w-72" placeholder="例如：我的 Codex" />
            <button class="btn btn-primary" :disabled="busy || !overview?.ready_for_key || !keyName.trim()" @click="createKey">创建 API Key</button>
          </div>
          <div v-if="createdKey" class="mt-4 rounded-xl border border-emerald-200 bg-emerald-50 p-4 dark:border-emerald-800 dark:bg-emerald-900/20">
            <p class="text-sm font-semibold text-emerald-800 dark:text-emerald-200">请立即复制并保存：</p>
            <code class="mt-2 block break-all text-sm text-emerald-900 dark:text-emerald-100">{{ createdKey }}</code>
          </div>
        </section>

        <section class="rounded-2xl border border-gray-200 bg-white p-6 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-400">第四步</p>
          <h2 class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">下载并配置 CC Switch</h2>
          <ol class="mt-4 list-decimal space-y-2 pl-5 text-sm leading-6 text-gray-600 dark:text-gray-300">
            <li>下载并安装 CC Switch。</li>
            <li>点击“导入至 CCS”，选择导入的来源并切换到它。</li>
            <li>重启 Codex，使新的 API 来源生效。</li>
          </ol>
          <div class="mt-4 flex flex-wrap gap-3">
            <a v-if="overview?.config.ccswitch_download_url" class="btn btn-secondary" :href="overview.config.ccswitch_download_url" target="_blank" rel="noreferrer">下载 CC Switch</a>
            <a v-if="importURL" class="btn btn-primary" :href="importURL">导入至 CCS</a>
          </div>
          <p v-if="!createdKey" class="mt-3 text-sm text-amber-600">创建 API Key 后才会生成导入链接。</p>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { buildGatewayUrl } from '@/api/client'
import {
  completeDiscordOnboarding,
  completeOpenAIOnboarding,
  getOnboardingOverview,
  startDiscordOnboarding,
  startOpenAIOnboarding,
  type OnboardingOAuthStart,
  type OnboardingUserOverview,
} from '@/api/user'
import { keysAPI } from '@/api/keys'
import { buildCcSwitchImportDeeplink, CC_SWITCH_USAGE_SCRIPT } from '@/utils/ccswitchImport'

const route = useRoute()
const router = useRouter()
const overview = ref<OnboardingUserOverview | null>(null)
const loading = ref(true)
const busy = ref(false)
const oauthStart = ref<OnboardingOAuthStart | null>(null)
const oauthSessionID = ref('')
const callbackURL = ref('')
const keyName = ref('我的 Codex')
const createdKey = ref('')

const redirectURI = computed(() => `${window.location.origin}/onboarding`)
const importURL = computed(() => {
  if (!createdKey.value) return ''
  return buildCcSwitchImportDeeplink({
    baseUrl: buildGatewayUrl('/v1'),
    clientType: 'gemini',
    providerName: 'Sub2API Codex',
    apiKey: createdKey.value,
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
  } finally {
    loading.value = false
  }
}

async function startDiscord() {
  busy.value = true
  try {
    const result = await startDiscordOnboarding(redirectURI.value)
    window.location.href = result.auth_url
  } finally {
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
    await router.replace('/onboarding')
    await load()
  } finally {
    busy.value = false
  }
}

async function startOpenAI() {
  busy.value = true
  try {
    oauthStart.value = await startOpenAIOnboarding(redirectURI.value)
    oauthSessionID.value = oauthStart.value.session_id
  } finally {
    busy.value = false
  }
}

async function completeOpenAI() {
  const parsed = new URL(callbackURL.value.trim())
  busy.value = true
  try {
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
  } finally {
    busy.value = false
  }
}

async function createKey() {
  busy.value = true
  try {
    const key = await keysAPI.create(keyName.value.trim())
    createdKey.value = key.key
    await load()
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await load()
  await completeDiscord()
})
</script>
