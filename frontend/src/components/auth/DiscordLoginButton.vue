<template>
  <div class="space-y-4">
    <div class="rounded-xl border border-indigo-200 bg-indigo-50/70 p-4 dark:border-indigo-900/70 dark:bg-indigo-950/30">
      <div class="flex items-start gap-3">
        <div class="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-indigo-600 text-lg font-bold text-white">D</div>
        <div>
          <h2 class="font-semibold text-gray-900 dark:text-white">使用 Discord 登录或注册</h2>
          <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">系统会使用 Discord 已验证的邮箱匹配账号。首次登录会自动创建账号，并加入指定服务器。</p>
        </div>
      </div>
    </div>
    <p v-if="!ready && !loading && !errorMessage" class="text-sm text-amber-600 dark:text-amber-400">Discord 登录尚未完成配置，请联系管理员。</p>
    <p v-if="errorMessage" class="text-sm leading-6 text-red-600 dark:text-red-400" role="alert">{{ errorMessage }}</p>
    <button type="button" class="btn btn-primary w-full" :disabled="disabled || loading || !ready" @click="start">
      <span v-if="loading" class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
      {{ loading ? '正在连接 Discord…' : '使用 Discord 登录或注册' }}
    </button>
    <p class="text-center text-xs leading-5 text-gray-500 dark:text-gray-400">无需设置密码。若邮箱与站内记录不一致，请联系管理员修改站内邮箱。</p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { completeDiscordLogin, getDiscordLoginStatus, persistOAuthTokenContext, startDiscordLogin } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

defineProps<{ disabled?: boolean }>()

const DISCORD_STATE_PREFIX = 'discord-login.'
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const ready = ref(false)
const loading = ref(false)
const errorMessage = ref('')

function errorText(error: unknown): string {
  const responseMessage = (error as { response?: { data?: { message?: string } } })?.response?.data?.message
  return responseMessage || (error instanceof Error ? error.message : 'Discord 登录失败，请重试。')
}

function callbackValue(name: string): string {
  const value = route.query[name]
  return Array.isArray(value) ? String(value[0] || '') : String(value || '')
}

async function clearCallbackQuery(): Promise<void> {
  const query = { ...route.query }
  delete query.code
  delete query.state
  delete query.error
  delete query.error_description
  await router.replace({ path: '/login', query })
}

async function finishCallback(): Promise<void> {
  const state = callbackValue('state')
  const code = callbackValue('code')
  if (!state.startsWith(DISCORD_STATE_PREFIX)) return
  if (!code) {
    errorMessage.value = callbackValue('error_description') || 'Discord 授权未完成，请重试。'
    await clearCallbackQuery()
    return
  }

  loading.value = true
  errorMessage.value = ''
  try {
    const tokens = await completeDiscordLogin(code, state)
    persistOAuthTokenContext(tokens)
    await authStore.setToken(tokens.access_token)
    await clearCallbackQuery()
    const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/')
      ? route.query.redirect
      : '/dashboard'
    await router.replace(redirect)
  } catch (error) {
    errorMessage.value = errorText(error)
    clearCallbackQuery()
  } finally {
    loading.value = false
  }
}

async function start(): Promise<void> {
  if (loading.value || !ready.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await startDiscordLogin()
    window.location.assign(result.authorize_url)
  } catch (error) {
    errorMessage.value = errorText(error)
    loading.value = false
  }
}

onMounted(async () => {
  try {
    const status = await getDiscordLoginStatus()
    ready.value = status.enabled && status.ready
  } catch (error) {
    ready.value = false
    errorMessage.value = errorText(error)
  }
  await finishCallback()
})
</script>
