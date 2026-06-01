import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

/**
 * Тесты CRUD-стора фильтров.
 *
 * Стратегия: мокаем апи-обёртки `apiGet/apiPost/apiPatch/apiDelete` (которые
 * сами под капотом дёргают axios). Это сохраняет тип-сейфти и тестирует ровно
 * нашу логику — упорядочивание, локальные state-мутации, дедупликацию.
 */

vi.mock('@/api/client', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiPatch: vi.fn(),
  apiDelete: vi.fn(),
}))

// Импорты ПОСЛЕ мока — иначе real-axios.
import { apiDelete, apiGet, apiPatch, apiPost } from '@/api/client'
import { useFiltersStore } from '@/stores/filters'
import type { Filter, FilterInput } from '@/types/api'

const mockedGet = apiGet as unknown as ReturnType<typeof vi.fn>
const mockedPost = apiPost as unknown as ReturnType<typeof vi.fn>
const mockedPatch = apiPatch as unknown as ReturnType<typeof vi.fn>
const mockedDelete = apiDelete as unknown as ReturnType<typeof vi.fn>

function makeFilter(id: string, overrides: Partial<Filter> = {}): Filter {
  return {
    id,
    user_id: 'user-1',
    section: 'Аэробика',
    teacher_uid: null,
    day_of_week: 'WEDNESDAY',
    time_from: '18:00',
    time_to: '21:00',
    min_rating: null,
    min_vacancy: 1,
    enabled: true,
    created_at: '2026-06-02T10:00:00Z',
    updated_at: '2026-06-02T10:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useFiltersStore — CRUD round-trip', () => {
  it('fetchAll: парсит { filters: [...] } из ответа gateway', async () => {
    mockedGet.mockResolvedValueOnce({ filters: [makeFilter('a'), makeFilter('b')] })
    const store = useFiltersStore()

    await store.fetchAll()

    expect(mockedGet).toHaveBeenCalledWith('/filters')
    expect(store.filters).toHaveLength(2)
    expect(store.filters.map((f) => f.id)).toEqual(['a', 'b'])
    expect(store.loading).toBe(false)
  })

  it('create: добавляет в начало списка', async () => {
    mockedGet.mockResolvedValueOnce({ filters: [makeFilter('a')] })
    const store = useFiltersStore()
    await store.fetchAll()

    const newFilter = makeFilter('new', { section: 'Йога' })
    mockedPost.mockResolvedValueOnce(newFilter)
    const input: FilterInput = {
      section: 'Йога',
      teacher_uid: null,
      day_of_week: 'ANY',
      time_from: null,
      time_to: null,
      min_rating: null,
      min_vacancy: 1,
      enabled: true,
    }

    const created = await store.create(input)

    expect(mockedPost).toHaveBeenCalledWith('/filters', input)
    expect(created.id).toBe('new')
    expect(store.filters.map((f) => f.id)).toEqual(['new', 'a'])
  })

  it('update: заменяет элемент в массиве по id', async () => {
    mockedGet.mockResolvedValueOnce({
      filters: [makeFilter('a', { section: 'A' }), makeFilter('b', { section: 'B' })],
    })
    const store = useFiltersStore()
    await store.fetchAll()

    const patched = makeFilter('a', { section: 'A-renamed' })
    mockedPatch.mockResolvedValueOnce(patched)

    await store.update('a', { section: 'A-renamed' })

    expect(mockedPatch).toHaveBeenCalledWith('/filters/a', { section: 'A-renamed' })
    expect(store.filters[0].section).toBe('A-renamed')
    expect(store.filters[1].section).toBe('B')
  })

  it('remove: убирает по id', async () => {
    mockedGet.mockResolvedValueOnce({
      filters: [makeFilter('a'), makeFilter('b')],
    })
    const store = useFiltersStore()
    await store.fetchAll()
    mockedDelete.mockResolvedValueOnce(undefined)

    await store.remove('a')

    expect(mockedDelete).toHaveBeenCalledWith('/filters/a')
    expect(store.filters.map((f) => f.id)).toEqual(['b'])
  })

  it('toggleEnabled: PATCH с одним полем enabled', async () => {
    mockedGet.mockResolvedValueOnce({
      filters: [makeFilter('a', { enabled: true })],
    })
    const store = useFiltersStore()
    await store.fetchAll()

    mockedPatch.mockResolvedValueOnce(makeFilter('a', { enabled: false }))

    await store.toggleEnabled('a', false)

    expect(mockedPatch).toHaveBeenCalledWith('/filters/a', { enabled: false })
    expect(store.filters[0].enabled).toBe(false)
  })

  it('fetchAll: graceful на отсутствие поля filters в ответе', async () => {
    mockedGet.mockResolvedValueOnce({} as unknown)
    const store = useFiltersStore()

    await store.fetchAll()

    expect(store.filters).toEqual([])
  })
})
