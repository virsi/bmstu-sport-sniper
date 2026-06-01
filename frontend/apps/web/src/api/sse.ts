import { apiPost } from '@/api/client'
import type { StreamTicket } from '@/types/api'

/**
 * Обработчик одного SSE-события `event:<name>`. Принимает уже распарсенный объект `T`.
 *
 * @typeParam T — тип payload-а.
 */
export type SseHandler<T> = (payload: T) => void

/** Колбэк смены состояния соединения. */
export type SseConnectionListener = (connected: boolean) => void

/** Опции для {@link openSseStream}. */
export interface SseOptions {
  /** URL endpoint-а. По умолчанию — `VITE_SSE_URL` или `/api/stream`. */
  url?: string
  /** Карта обработчиков именованных событий, ключ — имя event-а. */
  handlers: Record<string, SseHandler<unknown>>
  /** Колбэк изменения статуса соединения (open / close / reconnect). */
  onConnectionChange?: SseConnectionListener
  /** Стартовая задержка ретрая, мс. По умолчанию 1000. */
  initialBackoffMs?: number
  /** Потолок задержки ретрая, мс. По умолчанию 30000. */
  maxBackoffMs?: number
}

/** Хэндл активного соединения. Закрытие — единственный side-effect, который ему нужен. */
export interface SseStreamHandle {
  /** Закрывает соединение и отменяет планируемый reconnect. */
  close: () => void
}

/**
 * Получает одноразовый SSE-ticket с бэка для безопасного открытия EventSource.
 *
 * Зачем не отдавать JWT в query: долгоживущий JWT попал бы в access-логи прокси/CDN
 * (см. `docs/review-findings.md` #3). Ticket — короткоживущий (5 мин) одноразовый
 * capability-токен; даже залогированный, повторно его использовать нельзя.
 *
 * Endpoint защищён обычной Auth — то есть нужен валидный access-token в Authorization
 * header (apiPost его подставит из localStorage).
 */
async function fetchTicket(): Promise<string> {
  const t = await apiPost<StreamTicket>('/stream/ticket', {}, { silent: true })
  return t.ticket
}

/**
 * Открывает SSE-соединение к gateway с автоматическим реконнектом по экспоненте.
 *
 * Особенности:
 * - Перед каждым (включая первое) подключением запрашивает one-time ticket
 *   через `POST /api/stream/ticket` и передаёт его в `?ticket=<X>`. Старый ticket
 *   одноразовый, на reconnect получаем новый. См. {@link fetchTicket}.
 * - Каждое именованное событие парсится JSON-ом и роутится в свой `handlers[event]`.
 * - На ошибку — закрывается, ждёт backoff (1s → 2s → 4s → ... → maxBackoffMs),
 *   открывается снова. Backoff сбрасывается на успешном open.
 * - На события `ping` (heartbeat) ничего не делаем — open-callback уже обновил
 *   статус «connected».
 * - Если ticket-ручка вернула 401 (access-token истёк) — apiPost сам сделает refresh
 *   и повторит; если и refresh упал — emit `auth:logout`, мы получим ошибку и
 *   назначим ретрай (после повторного логина юзер пере-подключится).
 *
 * @example
 * const stream = openSseStream({
 *   handlers: {
 *     'new-slot': (e) => slotsStore.prepend((e as NewSlotEvent).slot),
 *   },
 *   onConnectionChange: (ok) => (slotsStore.connected = ok),
 * })
 * // ... позже
 * stream.close()
 */
export function openSseStream(opts: SseOptions): SseStreamHandle {
  const baseUrl = opts.url ?? import.meta.env.VITE_SSE_URL ?? '/api/stream'
  const initial = opts.initialBackoffMs ?? 1_000
  const max = opts.maxBackoffMs ?? 30_000

  let es: EventSource | null = null
  let retryTimer: number | null = null
  let backoff = initial
  let closed = false

  const notify = (connected: boolean): void => {
    if (opts.onConnectionChange) {
      opts.onConnectionChange(connected)
    }
  }

  const scheduleRetry = (): void => {
    if (closed) {
      return
    }
    const delay = backoff
    backoff = Math.min(backoff * 2, max)
    retryTimer = window.setTimeout(connect, delay)
  }

  const connect = async (): Promise<void> => {
    if (closed) {
      return
    }
    let ticket: string
    try {
      ticket = await fetchTicket()
    } catch (err) {
      // Не удалось получить ticket (401 / сеть / 5xx) — ретраим, не разрываем UX.
      notify(false)
      // eslint-disable-next-line no-console
      console.warn('[sse] failed to fetch ticket; will retry', err)
      scheduleRetry()
      return
    }
    if (closed) {
      // Юзер вызвал close() пока шёл ticket-запрос.
      return
    }

    const sep = baseUrl.includes('?') ? '&' : '?'
    const url = `${baseUrl}${sep}ticket=${encodeURIComponent(ticket)}`
    es = new EventSource(url)

    es.onopen = (): void => {
      backoff = initial
      notify(true)
    }

    es.onerror = (): void => {
      notify(false)
      if (es) {
        es.close()
        es = null
      }
      scheduleRetry()
    }

    for (const [eventName, handler] of Object.entries(opts.handlers)) {
      es.addEventListener(eventName, (ev) => {
        const me = ev as MessageEvent<string>
        // Heartbeat — data пустой, не парсим.
        if (!me.data) {
          return
        }
        try {
          const payload = JSON.parse(me.data) as unknown
          handler(payload)
        } catch (parseErr) {
          // Битый JSON — лог в консоль, соединение не рвём.
          // eslint-disable-next-line no-console
          console.error(`[sse] malformed payload for "${eventName}":`, parseErr)
        }
      })
    }
  }

  // Не await-им: ошибки внутри connect() сами планируют ретрай.
  void connect()

  return {
    close: (): void => {
      closed = true
      if (retryTimer !== null) {
        window.clearTimeout(retryTimer)
        retryTimer = null
      }
      if (es) {
        es.close()
        es = null
      }
      notify(false)
    },
  }
}
