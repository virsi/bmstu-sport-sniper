import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiGet } from '@/api/client'
import { openSseStream, type SseStreamHandle } from '@/api/sse'
import type { NewSlotEvent, Slot, SlotListResponse } from '@/types/api'

/**
 * Pinia-стор ленты слотов.
 *
 * Слоты приходят двумя путями:
 * 1. Стартовая загрузка через `GET /api/slots` (последние N матчей за окно).
 * 2. Live-стрим через SSE `event:new-slot` — новые добавляются в начало списка.
 *
 * Дубли по `slot.id` отбрасываются — backend тоже фильтрует, это страховка от
 * гонок (например, реконнект во время повторного push-а).
 */
export const useSlotsStore = defineStore('slots', () => {
  const slots = ref<Slot[]>([])
  const connected = ref(false)
  const loading = ref(false)
  /** ISO-8601 момент последнего успешного `GET /api/slots`. */
  const fetchedAt = ref<string | null>(null)
  const error = ref<string | null>(null)

  let stream: SseStreamHandle | null = null

  /**
   * Добавляет слот в начало списка, если такого id ещё нет.
   *
   * @internal — используется и из SSE-хэндлера, и из юнит-тестов.
   */
  function prepend(slot: Slot): void {
    if (slots.value.some((s) => s.id === slot.id)) {
      return
    }
    slots.value.unshift(slot)
  }

  /** Стартовая загрузка ленты с backend. */
  async function fetchInitial(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const res = await apiGet<SlotListResponse>('/slots')
      slots.value = res.slots ?? []
      fetchedAt.value = res.fetched_at ?? null
    } finally {
      loading.value = false
    }
  }

  /**
   * Открывает SSE-соединение и подписывается на `new-slot`. Идемпотентна —
   * повторный вызов закрывает старое соединение и открывает новое.
   *
   * Также слушает event `status` — пока пишет в console; будущее место для toast-ов.
   */
  function subscribe(): void {
    unsubscribe()
    stream = openSseStream({
      handlers: {
        'new-slot': (payload) => {
          const ev = payload as NewSlotEvent
          if (ev?.slot) {
            prepend(ev.slot)
          }
        },
        status: (payload) => {
          // eslint-disable-next-line no-console
          console.info('[sse] status event:', payload)
        },
      },
      onConnectionChange: (ok) => {
        connected.value = ok
      },
    })
  }

  /** Закрывает SSE-соединение, если оно открыто. */
  function unsubscribe(): void {
    if (stream) {
      stream.close()
      stream = null
    }
    connected.value = false
  }

  return {
    slots,
    connected,
    loading,
    fetchedAt,
    error,
    fetchInitial,
    subscribe,
    unsubscribe,
    prepend,
  }
})
