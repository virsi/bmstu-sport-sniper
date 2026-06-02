import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { apiDelete, apiGet, apiPost } from '@/api/client'
import type { BmstuCredentials, BmstuStatus, BmstuStatusFlags } from '@/types/api'

/**
 * Pinia-стор линковки LKS-кредов БМГТУ.
 *
 * Хранит только статус (linked/valid/last_login_at/health_group) — сами
 * креды не передаются на фронт никогда. Логин/пароль улетают POST-ом, ответ —
 * только статус.
 */
export const useBmstuStore = defineStore('bmstu', () => {
  const status = ref<BmstuStatus>({ status: 'NOT_LINKED' })
  const loading = ref(false)
  const error = ref<string | null>(null)

  /** Удобные boolean-флаги поверх enum `status`. */
  const flags = computed<BmstuStatusFlags>(() => ({
    linked: status.value.status !== 'NOT_LINKED',
    valid: status.value.status === 'VALID',
  }))

  /** Грузит актуальный статус с backend. */
  async function fetchStatus(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      status.value = await apiGet<BmstuStatus>('/bmstu/status', { silent: true })
    } finally {
      loading.value = false
    }
  }

  /**
   * Сохраняет/обновляет креды LKS вместе с выбранной группой здоровья.
   * Backend проверяет логин синхронно — после 204 имеет смысл сразу обновить
   * статус (включая `health_group`, который вернётся в GET /bmstu/status).
   *
   * Ошибки:
   *   - 400 — невалидный `health_group` или пустой login/password.
   *   - 401 — креды отвергнуты Keycloak.
   *   - 503 — LKS недоступен.
   */
  async function setCreds(creds: BmstuCredentials): Promise<void> {
    await apiPost<void, BmstuCredentials>('/bmstu/creds', creds, { silent: true })
    await fetchStatus()
  }

  /** Удаляет креды на backend и сбрасывает локальный статус. */
  async function deleteCreds(): Promise<void> {
    await apiDelete<void>('/bmstu/creds')
    status.value = { status: 'NOT_LINKED' }
  }

  return { status, loading, error, flags, fetchStatus, setCreds, deleteCreds }
})
