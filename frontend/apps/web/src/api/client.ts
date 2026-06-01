import axios, {
  type AxiosError,
  type AxiosInstance,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios'
import type { AccessToken, ProblemDetails } from '@/types/api'

/**
 * Ключ в localStorage для access-токена.
 *
 * @remarks
 * Refresh-токен живёт в httpOnly cookie `rt`, недоступной из JS (защита от XSS).
 * Access-токен короткоживущий (15 мин) и не даёт долгосрочного доступа даже
 * при утечке через XSS — поэтому остаётся в localStorage для удобства SPA.
 *
 * Cookie ставит/удаляет backend на /api/auth/{login,refresh,logout}.
 * См. `docs/api.md` секция 1 и `docs/review-findings.md` #2 (resolved 2026-06-02).
 */
const ACCESS_KEY = 'fizcultor:access'

const BASE_URL = import.meta.env.VITE_API_BASE_URL ?? '/api'

/** Расширение конфига запроса флагом, чтобы не зацикливать refresh. */
interface RetryableRequest extends InternalAxiosRequestConfig {
  _retry?: boolean
  /** Подавить авто-toast на ошибку (например, для silent calls /me на старте). */
  _silent?: boolean
}

/** Опции расширения axios-конфига, доступные на каждом вызове. */
export interface RequestOptions extends AxiosRequestConfig {
  /** Не показывать автоматический toast при ошибке. */
  silent?: boolean
}

/**
 * Читает access-токен из localStorage.
 *
 * Вынесено в функцию (а не одноразово прочитано в модуле), чтобы переживать
 * мутации между табами и логин/логаут без перезагрузки страницы.
 */
export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_KEY)
}

/**
 * Кладёт access-токен в localStorage.
 *
 * Refresh-токен НЕ принимается: он живёт в httpOnly cookie, ставится бэком
 * автоматически на /api/auth/{login,refresh}.
 *
 * @param tokens — ответ login/refresh от gateway-svc.
 */
export function setAccess(tokens: AccessToken): void {
  localStorage.setItem(ACCESS_KEY, tokens.access_token)
}

/**
 * Очищает access-токен в localStorage.
 *
 * Refresh cookie удаляется бэкендом на logout (Set-Cookie: rt=; Max-Age=-1).
 * Если хотите принудительно удалить cookie из клиента — вызовите logout endpoint.
 */
export function clearTokens(): void {
  localStorage.removeItem(ACCESS_KEY)
}

/**
 * Глобальный axios-клиент для общения с gateway-svc.
 *
 * Особенности:
 * - Автоматически подставляет `Authorization: Bearer <access>` в заголовки.
 * - `withCredentials: true` — браузер шлёт cookie `rt` (refresh) на /api/auth/*.
 * - На 401 единожды пытается обновить токены через `/auth/refresh` (cookie уезжает
 *   автоматом, тело пустое) и повторяет запрос.
 * - На неуспешный refresh — чистит access и эмиттит CustomEvent `auth:logout`,
 *   чтобы Pinia-стор/роутер могли среагировать (редирект на /login).
 * - На любой 4xx/5xx (если не передан `silent: true`) показывает toast с
 *   user-friendly текстом, извлечённым из RFC7807 ProblemDetails.
 *
 * Использовать ТОЛЬКО этот клиент — никаких сырых fetch/axios в компонентах.
 */
export const apiClient: AxiosInstance = axios.create({
  baseURL: BASE_URL,
  timeout: 15_000,
  // Critical: чтобы браузер отправлял cookie `rt` на /api/auth/{refresh,logout}
  // и принимал Set-Cookie с /login. На same-origin это поведение по умолчанию,
  // но при разных origin (dev: 5173 → 8080 через proxy; prod: разные subdomains)
  // нужен явный флаг.
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

apiClient.interceptors.request.use((config) => {
  const token = getAccessToken()
  if (token && config.headers) {
    config.headers.Authorization = `Bearer ${token}`
  }
  // Прокинем флаг silent из config.silent в внутренний _silent (типизация cleaner).
  const opts = config as RetryableRequest & { silent?: boolean }
  if (opts.silent) {
    opts._silent = true
  }
  return config
})

/** Промис активного refresh-запроса — чтобы параллельные 401 не плодили N рефрешей. */
let refreshInFlight: Promise<AccessToken> | null = null

/**
 * Запрос на обновление токенов. Возвращает существующий промис, если refresh уже идёт.
 *
 * Не использует общий axios-клиент (чтобы не попасть в собственный 401-handler).
 * Тело запроса пустое — refresh передаётся браузером через cookie `rt` благодаря
 * `withCredentials: true`.
 */
async function refreshTokens(): Promise<AccessToken> {
  if (refreshInFlight) {
    return refreshInFlight
  }
  refreshInFlight = axios
    .post<AccessToken>(
      `${BASE_URL}/auth/refresh`,
      {},
      {
        headers: { 'Content-Type': 'application/json' },
        withCredentials: true,
      },
    )
    .then((res) => {
      setAccess(res.data)
      return res.data
    })
    .finally(() => {
      refreshInFlight = null
    })
  return refreshInFlight
}

/**
 * Извлекает user-friendly сообщение из любой ошибки axios.
 *
 * Порядок: ProblemDetails.detail → ProblemDetails.title → axios.message → fallback.
 */
export function extractErrorMessage(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as ProblemDetails | undefined
    if (data) {
      if (typeof data.detail === 'string' && data.detail.trim()) {
        return data.detail
      }
      if (typeof data.title === 'string' && data.title.trim()) {
        return data.title
      }
    }
    return err.message
  }
  if (err instanceof Error) {
    return err.message
  }
  return 'Неизвестная ошибка'
}

/**
 * Глобальный toaster-эмиттер.
 *
 * Не импортируем `useToast` напрямую (цикл импортов: client ↔ useToast не нужен).
 * Вместо этого эмиттим CustomEvent — слушает его `ToastContainer` / любая обёртка.
 */
function emitErrorToast(message: string): void {
  if (typeof window === 'undefined') {
    return
  }
  window.dispatchEvent(new CustomEvent('api:error', { detail: { message } }))
}

apiClient.interceptors.response.use(
  (response) => response,
  async (error: AxiosError<ProblemDetails>) => {
    const original = error.config as RetryableRequest | undefined
    const status = error.response?.status

    // 401 → пытаемся обновиться один раз. Refresh-эндпоинт исключаем (иначе рекурсия).
    const url = original?.url ?? ''
    const isRefreshCall = url.includes('/auth/refresh')
    if (status === 401 && original && !original._retry && !isRefreshCall) {
      original._retry = true
      try {
        const tokens = await refreshTokens()
        if (original.headers) {
          original.headers.Authorization = `Bearer ${tokens.access_token}`
        }
        return apiClient.request(original)
      } catch (refreshErr) {
        clearTokens()
        if (typeof window !== 'undefined') {
          window.dispatchEvent(new CustomEvent('auth:logout'))
        }
        return Promise.reject(refreshErr)
      }
    }

    // Авто-toast (если не silent и не auth-эндпоинт, который сам показывает ошибку формы).
    const isAuthFlow = url.startsWith('/auth/')
    if (!original?._silent && !isAuthFlow && status && status >= 400) {
      emitErrorToast(extractErrorMessage(error))
    }

    return Promise.reject(error)
  },
)

/**
 * Утилита для типизированного GET.
 *
 * @typeParam T — тип ответа.
 * @param url — endpoint после `baseURL`.
 * @param config — опционально, прокидывается в axios (+ `silent`).
 */
export async function apiGet<T>(url: string, config?: RequestOptions): Promise<T> {
  const res = await apiClient.get<T>(url, config)
  return res.data
}

/** Типизированный POST. */
export async function apiPost<T, B = unknown>(
  url: string,
  body?: B,
  config?: RequestOptions,
): Promise<T> {
  const res = await apiClient.post<T>(url, body, config)
  return res.data
}

/** Типизированный PATCH. */
export async function apiPatch<T, B = unknown>(
  url: string,
  body?: B,
  config?: RequestOptions,
): Promise<T> {
  const res = await apiClient.patch<T>(url, body, config)
  return res.data
}

/** Типизированный DELETE (часто без тела ответа). */
export async function apiDelete<T = void>(url: string, config?: RequestOptions): Promise<T> {
  const res = await apiClient.delete<T>(url, config)
  return res.data
}
