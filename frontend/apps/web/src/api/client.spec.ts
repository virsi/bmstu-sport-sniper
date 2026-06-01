import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import axios from 'axios'

/**
 * Тесты axios-клиента: проверяем 401-retry + dedupe параллельных refresh-ов
 * в cookie-based mode.
 *
 * Подход: моки на низком уровне через `axios.create.mockReturnValue(mockInstance)`
 * не подходят (interceptor-логика живёт ВНУТРИ apiClient). Поэтому мокаем
 * `axios.post` для refresh-эндпоинта и `mockInstance.get` для основного запроса,
 * получив реальный экземпляр через импорт.
 */

interface MockedAxios {
  post: ReturnType<typeof vi.fn>
}

// Замокаем axios до импорта клиента.
vi.mock('axios', async () => {
  const actual = await vi.importActual<typeof import('axios')>('axios')
  // Хитрость: оставляем `create`, `isAxiosError`, interceptors из реального axios,
  // но перехватываем post для refresh-эндпоинта.
  const patched = {
    ...actual.default,
    post: vi.fn(),
  }
  return {
    ...actual,
    default: patched,
  }
})

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('apiClient — token handling', () => {
  it('подставляет Authorization header из getAccessToken', async () => {
    const { apiClient, setAccess } = await import('@/api/client')
    setAccess({
      access_token: 'AT-1',
      access_expires_at: '2030-01-01T00:00:00Z',
      refresh_expires_at: '2030-02-01T00:00:00Z',
    })

    // Перехватим adapter, чтобы не делать сеть.
    const adapter = vi.fn().mockResolvedValue({
      data: { ok: true },
      status: 200,
      statusText: 'OK',
      headers: {},
      config: {},
    })

    const res = await apiClient.get('/me', { adapter })
    expect(res.data).toEqual({ ok: true })
    const calledWith = adapter.mock.calls[0][0]
    expect(calledWith.headers.Authorization).toBe('Bearer AT-1')
  })

  it('apiClient создан с withCredentials: true (cookies для refresh)', async () => {
    const { apiClient } = await import('@/api/client')
    expect(apiClient.defaults.withCredentials).toBe(true)
  })

  it('clearTokens очищает access (refresh живёт в cookie, чистится бэком)', async () => {
    const { setAccess, clearTokens, getAccessToken } = await import('@/api/client')
    setAccess({
      access_token: 'AT',
      access_expires_at: '2030-01-01T00:00:00Z',
      refresh_expires_at: '2030-02-01T00:00:00Z',
    })
    expect(getAccessToken()).toBe('AT')
    clearTokens()
    expect(getAccessToken()).toBeNull()
  })
})

describe('apiClient — 401 refresh flow', () => {
  it('на 401 один раз пытается обновить токены и повторяет запрос (refresh без body)', async () => {
    const { apiClient, setAccess, getAccessToken } = await import('@/api/client')
    setAccess({
      access_token: 'OLD',
      access_expires_at: '2030-01-01T00:00:00Z',
      refresh_expires_at: '2030-02-01T00:00:00Z',
    })

    // Мокаем refresh — axios.post вернёт новый access.
    const mockedAxios = axios as unknown as MockedAxios
    mockedAxios.post.mockResolvedValueOnce({
      data: {
        access_token: 'NEW',
        access_expires_at: '2030-01-01T00:00:00Z',
        refresh_expires_at: '2030-02-01T00:00:00Z',
      },
    })

    // Adapter: первый вызов с OLD → 401, второй с NEW → 200.
    let callCount = 0
    const adapter = vi.fn().mockImplementation((config: { headers: Record<string, string> }) => {
      callCount++
      if (callCount === 1) {
        return Promise.reject({
          isAxiosError: true,
          config,
          response: { status: 401, data: { title: 'Unauthorized', status: 401 } },
        })
      }
      return Promise.resolve({
        data: { protected: true },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      })
    })

    const res = await apiClient.get('/secure', { adapter })
    expect(res.data).toEqual({ protected: true })
    expect(adapter).toHaveBeenCalledTimes(2)
    expect(mockedAxios.post).toHaveBeenCalledTimes(1)

    // Critical: refresh должен идти с пустым body (refresh в cookie) и withCredentials.
    const [refreshUrl, refreshBody, refreshConfig] = mockedAxios.post.mock.calls[0]
    expect(String(refreshUrl)).toContain('/auth/refresh')
    expect(refreshBody).toEqual({})
    expect(refreshConfig).toMatchObject({ withCredentials: true })

    // После refresh access обновился.
    expect(getAccessToken()).toBe('NEW')
  })

  it('dedupe: параллельные 401 делают ровно один refresh-запрос', async () => {
    const { apiClient, setAccess } = await import('@/api/client')
    setAccess({
      access_token: 'OLD',
      access_expires_at: '2030-01-01T00:00:00Z',
      refresh_expires_at: '2030-02-01T00:00:00Z',
    })

    const mockedAxios = axios as unknown as MockedAxios
    // Один общий ответ refresh-а.
    mockedAxios.post.mockResolvedValueOnce({
      data: {
        access_token: 'NEW',
        access_expires_at: '2030-01-01T00:00:00Z',
        refresh_expires_at: '2030-02-01T00:00:00Z',
      },
    })

    // Adapter: первый вызов любого запроса = 401, потом 200.
    const callsPerUrl: Record<string, number> = {}
    const adapter = vi.fn().mockImplementation(
      (config: { url: string; headers: Record<string, string> }) => {
        const u = config.url
        callsPerUrl[u] = (callsPerUrl[u] ?? 0) + 1
        if (callsPerUrl[u] === 1) {
          return Promise.reject({
            isAxiosError: true,
            config,
            response: { status: 401, data: { title: 'Unauthorized', status: 401 } },
          })
        }
        return Promise.resolve({
          data: { url: u },
          status: 200,
          statusText: 'OK',
          headers: {},
          config,
        })
      },
    )

    const [a, b, c] = await Promise.all([
      apiClient.get('/a', { adapter }),
      apiClient.get('/b', { adapter }),
      apiClient.get('/c', { adapter }),
    ])
    expect(a.data).toEqual({ url: '/a' })
    expect(b.data).toEqual({ url: '/b' })
    expect(c.data).toEqual({ url: '/c' })

    // Самое важное: refresh вызван РОВНО ОДИН раз, несмотря на 3 параллельных 401.
    expect(mockedAxios.post).toHaveBeenCalledTimes(1)
  })

  it('извлекает detail из RFC7807 problem+json', async () => {
    const { extractErrorMessage } = await import('@/api/client')
    const err = {
      isAxiosError: true,
      response: {
        data: { title: 'Invalid', status: 400, detail: 'Email уже занят' },
      },
      message: 'Request failed',
    }
    // axios.isAxiosError вернёт true для подобной формы (т.к. мокнули actual).
    Object.defineProperty(err, 'name', { value: 'AxiosError' })
    const msg = extractErrorMessage(err)
    expect(msg).toBe('Email уже занят')
  })

  it('фолбэк на title если detail пустой', async () => {
    const { extractErrorMessage } = await import('@/api/client')
    const err = {
      isAxiosError: true,
      response: {
        data: { title: 'Not found', status: 404 },
      },
      message: 'Request failed',
    }
    const msg = extractErrorMessage(err)
    expect(msg).toBe('Not found')
  })
})
