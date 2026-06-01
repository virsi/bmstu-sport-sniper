/**
 * slots-sse.spec.ts — verifies the Dashboard SSE flow.
 *
 *  - The dashboard subscribes to /api/stream.
 *  - When the server pushes an `event:new-slot`, the slot card appears.
 *  - The "Live-канал активен" indicator turns on.
 *
 * Implementation detail: Playwright `route` cannot stream chunks while
 * keeping the connection open the way EventSource expects. Instead we
 * shim window.EventSource in the page context so we can fire events
 * deterministically from the test.
 */
import { test, expect } from '@playwright/test'
import { installGatewayMocks, newBackendState } from './fixtures/mocks'

const seededUser = {
  id: '7',
  email: 'qa@example.com',
  created_at: new Date().toISOString(),
  last_seen_at: new Date().toISOString(),
}

test.describe('Slots SSE stream', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem('fizcultor:access', 'fake-access')
      localStorage.setItem('fizcultor:refresh', 'fake-refresh')
    })

    // Replace EventSource with a controllable stub. The SPA's openSseStream
    // calls `new EventSource(url)` then attaches handlers via addEventListener.
    // Our stub captures the last instance on window.__sseTest so the test can
    // call dispatchEvent('new-slot', payload).
    await page.addInitScript(() => {
      type Listener = (e: MessageEvent) => void
      class FakeEventSource extends EventTarget {
        url: string
        readyState = 0
        listeners: Record<string, Listener[]> = {}
        constructor(url: string) {
          super()
          this.url = url
          // Mark connected on the next tick so the "open" handler fires after
          // the SPA finished attaching listeners.
          setTimeout(() => {
            this.readyState = 1
            this.dispatchEvent(new Event('open'))
          }, 10)
          ;(window as unknown as { __sseTest: FakeEventSource }).__sseTest = this
        }
        addEventListener(name: string, cb: EventListenerOrEventListenerObject): void {
          const fn = (cb as Listener) ?? (() => undefined)
          this.listeners[name] ??= []
          this.listeners[name].push(fn)
          super.addEventListener(name, cb)
        }
        close(): void {
          this.readyState = 2
        }
      }
      (window as unknown as { EventSource: typeof EventSource }).EventSource =
        FakeEventSource as unknown as typeof EventSource
    })
  })

  test('should show the connection indicator after subscribe', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'p', user: seededUser })
    await installGatewayMocks(page, state)

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Лента слотов' })).toBeVisible()

    // After the fake EventSource fires 'open', the SPA flips connected=true.
    await expect(page.getByText(/Live-канал активен/i)).toBeVisible()
  })

  test('should append a new slot card when server pushes new-slot event', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'p', user: seededUser })
    // Have one filter so the "no filters" empty state is NOT shown.
    state.filters.push({
      id: 'f1',
      user_id: '7',
      section: 'Аэробика',
      teacher_uid: null,
      day_of_week: null,
      time_from: null,
      time_to: null,
      min_rating: null,
      min_vacancy: 1,
      enabled: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    await installGatewayMocks(page, state)

    await page.goto('/')
    await expect(page.getByText(/Live-канал активен/i)).toBeVisible()

    // Fire a new-slot event from the test side.
    await page.evaluate(() => {
      const es = (window as unknown as { __sseTest: EventTarget }).__sseTest
      const payload = JSON.stringify({
        slot: {
          id: 'live-slot-1',
          week: 7,
          time: '10:00-11:30',
          place: 'ГЗ-3',
          teacher_name: 'Сергеев С.С.',
          section: 'Аэробика',
          vacancy: 4,
          semester_uuid: 'test-sem',
        },
      })
      const evt = new MessageEvent('new-slot', { data: payload })
      es.dispatchEvent(evt)
    })

    // The slot card must appear on the dashboard.
    await expect(page.getByText('Сергеев С.С.')).toBeVisible()
    await expect(page.getByText('ГЗ-3')).toBeVisible()
  })

  test('should deduplicate slots with the same id pushed twice', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'p', user: seededUser })
    state.filters.push({
      id: 'f1',
      user_id: '7',
      section: 'Аэробика',
      teacher_uid: null,
      day_of_week: null,
      time_from: null,
      time_to: null,
      min_rating: null,
      min_vacancy: 1,
      enabled: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    })
    await installGatewayMocks(page, state)

    await page.goto('/')
    await expect(page.getByText(/Live-канал активен/i)).toBeVisible()

    const fire = async () =>
      page.evaluate(() => {
        const es = (window as unknown as { __sseTest: EventTarget }).__sseTest
        const payload = JSON.stringify({
          slot: {
            id: 'dup-slot',
            week: 7,
            time: '08:30-10:00',
            place: 'Зал A',
            teacher_name: 'Дублёр Д.Д.',
            section: 'Аэробика',
            vacancy: 2,
            semester_uuid: 'test-sem',
          },
        })
        es.dispatchEvent(new MessageEvent('new-slot', { data: payload }))
      })

    await fire()
    await fire()

    // Exactly one card with the duplicate teacher name — store.prepend dedupes.
    const cards = page.getByText('Дублёр Д.Д.')
    await expect(cards).toHaveCount(1)
  })
})
