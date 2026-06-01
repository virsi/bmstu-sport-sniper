import { ref, readonly, type Ref, type DeepReadonly } from 'vue'

/** Один тип уведомления. */
export type ToastKind = 'info' | 'success' | 'error'

/** Тост-объект во внутреннем реестре. */
export interface Toast {
  /** Уникальный id — увеличивающийся счётчик. */
  id: number
  /** Тип — определяет цвет. */
  kind: ToastKind
  /** Текст, который видит юзер. */
  message: string
}

const toasts = ref<Toast[]>([])
let nextId = 1

/**
 * Минималистичный toast-нотифаер для всего SPA.
 *
 * Singleton — состояние живёт на уровне модуля, чтобы любой компонент или composable
 * мог пушить уведомления, а единственный `<ToastContainer>` в App.vue их рендерил.
 *
 * @example
 * const { error } = useToast()
 * try { ... } catch (e) { error(extractErrorMessage(e)) }
 */
export function useToast(): {
  toasts: DeepReadonly<Ref<Toast[]>>
  info: (msg: string) => void
  success: (msg: string) => void
  error: (msg: string) => void
  dismiss: (id: number) => void
} {
  function push(kind: ToastKind, message: string): void {
    const id = nextId++
    toasts.value.push({ id, kind, message })
    // Авто-снос через 5 секунд (для ошибок — 8).
    const ttl = kind === 'error' ? 8_000 : 5_000
    window.setTimeout(() => dismiss(id), ttl)
  }

  function dismiss(id: number): void {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return {
    toasts: readonly(toasts),
    info: (m) => push('info', m),
    success: (m) => push('success', m),
    error: (m) => push('error', m),
    dismiss,
  }
}
