import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiDelete, apiGet, apiPatch, apiPost } from '@/api/client'
import type { Filter, FilterInput, FilterListResponse } from '@/types/api'

/**
 * Pinia-стор фильтров.
 *
 * CRUD-обёртка над `/api/filters`. Все мутации идут через backend — локального
 * оптимистичного state-а пока нет (KISS; добавим если будет заметный лаг).
 */
export const useFiltersStore = defineStore('filters', () => {
  const filters = ref<Filter[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  /** Подтягивает список фильтров с backend. */
  async function fetchAll(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const res = await apiGet<FilterListResponse>('/filters')
      filters.value = res.filters ?? []
    } finally {
      loading.value = false
    }
  }

  /**
   * Создаёт новый фильтр.
   *
   * @returns созданный фильтр с серверным id.
   */
  async function create(input: FilterInput): Promise<Filter> {
    const created = await apiPost<Filter, FilterInput>('/filters', input)
    filters.value.unshift(created)
    return created
  }

  /**
   * Частичное обновление фильтра.
   *
   * @param id — UUID существующего фильтра.
   * @param patch — поля для изменения.
   */
  async function update(id: string, patch: Partial<FilterInput>): Promise<Filter> {
    const updated = await apiPatch<Filter, Partial<FilterInput>>(`/filters/${id}`, patch)
    const idx = filters.value.findIndex((f) => f.id === id)
    if (idx >= 0) {
      filters.value[idx] = updated
    }
    return updated
  }

  /** Удаляет фильтр. После 204 убирает из локального списка. */
  async function remove(id: string): Promise<void> {
    await apiDelete<void>(`/filters/${id}`)
    filters.value = filters.value.filter((f) => f.id !== id)
  }

  /**
   * Тоггл активности фильтра — частый кейс «выключить, не удаляя».
   * Просто обёртка над {@link update}.
   */
  async function toggleEnabled(id: string, enabled: boolean): Promise<Filter> {
    return update(id, { enabled })
  }

  return { filters, loading, error, fetchAll, create, update, remove, toggleEnabled }
})
