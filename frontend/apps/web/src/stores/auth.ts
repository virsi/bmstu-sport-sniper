import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { apiGet, apiPost, clearTokens, getAccessToken, setAccess } from '@/api/client'
import type { AccessToken, TelegramLinkInit, User } from '@/types/api'

/**
 * Pinia-стор аутентификации.
 *
 * Хранит JWT access и профиль текущего юзера. Access физически живёт в localStorage
 * (см. {@link setAccess}); этот стор — реактивная обёртка + методы.
 *
 * Refresh-токен в этом сторе НЕ хранится — он в httpOnly cookie, недоступной из JS.
 * См. `src/api/client.ts` и `docs/api.md` секция 1.
 */
export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref<string | null>(getAccessToken())
  const user = ref<User | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  /** Залогинен ли юзер (есть access — proof валидности на стороне backend). */
  const isAuthenticated = computed<boolean>(() => Boolean(accessToken.value))

  /**
   * Сохраняет access-токен в стор и localStorage синхронно.
   * Refresh-cookie ставит бэк (Set-Cookie на login/refresh).
   *
   * @param tokens — ответ от auth endpoints gateway-svc.
   */
  function applyAccess(tokens: AccessToken): void {
    accessToken.value = tokens.access_token
    setAccess(tokens)
  }

  /**
   * Регистрирует нового пользователя.
   *
   * Сразу после успеха надо вызвать {@link login} — register не выдаёт токены
   * (см. api.md секция 1.1).
   *
   * @param email — email-логин.
   * @param password — пароль (валидация длины — на форме через zod).
   * @throws AxiosError при 4xx/5xx.
   */
  async function register(email: string, password: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      await apiPost<User>('/auth/register', { email, password }, { silent: true })
    } finally {
      loading.value = false
    }
  }

  /**
   * Логин по email+password. Сохраняет access + подтягивает профиль.
   * Refresh-cookie ставит бэк автоматически (Set-Cookie).
   *
   * @throws AxiosError на неверные креды (всегда 401, без различения причины).
   */
  async function login(email: string, password: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const tokens = await apiPost<AccessToken>(
        '/auth/login',
        { email, password },
        { silent: true },
      )
      applyAccess(tokens)
      await fetchMe()
    } finally {
      loading.value = false
    }
  }

  /**
   * Принудительный refresh — обычно делать не нужно (это умеет axios-interceptor),
   * но полезно, например, при возврате из фоновой вкладки.
   *
   * Body пустой; refresh идёт в cookie благодаря `withCredentials: true` в axios.
   */
  async function refresh(): Promise<void> {
    const tokens = await apiPost<AccessToken>('/auth/refresh', {}, { silent: true })
    applyAccess(tokens)
  }

  /**
   * Загружает профиль текущего пользователя в `user`.
   *
   * Должна вызываться после login или при инициализации приложения, если access уже есть.
   */
  async function fetchMe(): Promise<void> {
    user.value = await apiGet<User>('/me', { silent: true })
  }

  /**
   * Стартует привязку Telegram. Возвращает deeplink — фронт открывает его в новой
   * вкладке. Сам факт привязки случится асинхронно, когда юзер нажмёт /start у бота.
   * Состояние линковки видно через {@link fetchMe} (поле `telegram_chat_id`).
   *
   * @returns объект с deeplink, кодом и временем истечения.
   */
  async function linkTelegram(): Promise<TelegramLinkInit> {
    return apiPost<TelegramLinkInit>('/me/telegram/init', undefined, { silent: true })
  }

  /**
   * Локальный logout: пытается дёрнуть backend (revoke refresh + delete cookie),
   * но даже на ошибку чистит стор и localStorage — пользователь должен оказаться
   * разлогинен в UI.
   *
   * Refresh-cookie передаётся автоматически (withCredentials: true) — body пустой.
   */
  async function logout(): Promise<void> {
    try {
      await apiPost<void>('/auth/logout', {}, { silent: true })
    } catch {
      // ignore — всё равно чистим локально
    }
    accessToken.value = null
    user.value = null
    clearTokens()
  }

  /**
   * Хук на глобальное событие `auth:logout` (эмиттится axios-interceptor-ом
   * после неуспешного refresh). Чистит локальное состояние без HTTP-запроса.
   */
  function handleForcedLogout(): void {
    accessToken.value = null
    user.value = null
  }

  return {
    accessToken,
    user,
    loading,
    error,
    isAuthenticated,
    register,
    login,
    refresh,
    fetchMe,
    linkTelegram,
    logout,
    handleForcedLogout,
  }
})
