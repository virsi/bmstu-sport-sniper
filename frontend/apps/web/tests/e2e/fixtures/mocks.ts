/**
 * mocks.ts — shared Playwright `page.route()` helpers that stub gateway-svc.
 *
 * Why route-level mocks and not a separate backend? Because:
 *  - E2E here verifies the *frontend* contract (UI flow + axios + Pinia +
 *    router behaviour), not backend correctness. Backend is covered by Go
 *    unit + integration tests.
 *  - Spinning up gateway-svc + Postgres + NATS for a Playwright run is slow
 *    and brittle; the routes here let any developer run E2E with just
 *    `pnpm exec playwright test`.
 *
 * Each helper returns the Playwright route handler that the spec installs
 * before the page.goto(). State is captured in closures so tests can assert
 * what the frontend actually sent.
 */
import type { Page, Route } from '@playwright/test'

/**
 * LocalStorage key used by the SPA for the access token.
 *
 * Mirrors {@link src/api/client.ts#ACCESS_KEY}. Kept here as a literal
 * to avoid pulling SPA modules into Playwright TS config.
 */
export const ACCESS_TOKEN_KEY = 'fizcultor:access'

/** RegisteredUser is the response shape from POST /auth/register. */
export interface RegisteredUser {
  id: string
  email: string
  created_at: string
  last_seen_at: string
}

/** TokenPair mirrors the proto contract used by login / refresh.
 *
 * Note: real `/auth/login` and `/auth/refresh` return only `access_token` in body
 * (refresh lives in httpOnly cookie). We keep `refresh_token` here for the mock
 * so it can simulate the backend state, but the SPA never reads it from the JSON.
 */
export interface TokenPair {
  access_token: string
  refresh_token: string
  access_expires_at: string
  refresh_expires_at: string
}

/** AccessTokenResponse — реальный shape login/refresh ответа (без refresh_token). */
export interface AccessTokenResponse {
  access_token: string
  access_expires_at: string
  refresh_expires_at: string
}

/** Filter mirrors common.v1.Filter as seen by the SPA. */
export interface FakeFilter {
  id: string
  user_id: string
  section: string | null
  teacher_uid: string | null
  day_of_week: string | null
  time_from: string | null
  time_to: string | null
  min_rating: number | null
  min_vacancy: number
  enabled: boolean
  created_at: string
  updated_at: string
}

/** BackendState mirrors the (relevant) state of gateway-svc per test run. */
export interface BackendState {
  users: Map<string, { password: string; user: RegisteredUser }>
  bmstuLinked: boolean
  filters: FakeFilter[]
  /** Last-issued access token; the dashboard route validates against this. */
  currentAccess: string
  /** Last-issued refresh token. */
  currentRefresh: string
  /** Auto-incrementing IDs for newly created resources. */
  nextID: number
}

/** newBackendState returns a fresh state for one test. */
export function newBackendState(): BackendState {
  return {
    users: new Map(),
    bmstuLinked: false,
    filters: [],
    currentAccess: '',
    currentRefresh: '',
    nextID: 1,
  }
}

/** issueToken bumps the in-memory state and returns the access response sent in JSON.
 *
 * The refresh token is tracked in `state.currentRefresh` to mirror cookie state;
 * the real gateway puts it in `Set-Cookie: rt=...`. For Playwright tests we don't
 * have a HTTP-stack cookie jar through `page.route()`, so the mock's refresh
 * endpoint just accepts any non-empty currentRefresh.
 */
function issueToken(state: BackendState): AccessTokenResponse {
  state.currentAccess = `access-token-${Date.now()}-${state.nextID++}`
  state.currentRefresh = `refresh-token-${Date.now()}-${state.nextID++}`
  return {
    access_token: state.currentAccess,
    access_expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
    refresh_expires_at: new Date(Date.now() + 7 * 24 * 60 * 60_000).toISOString(),
  }
}

/**
 * installGatewayMocks wires page.route() handlers for every REST endpoint
 * the SPA touches. Pass a {@link BackendState} so the test can both seed
 * preconditions and read state after interactions.
 */
export async function installGatewayMocks(page: Page, state: BackendState): Promise<void> {
  await page.route('**/api/**', async (route: Route) => {
    const url = new URL(route.request().url())
    const method = route.request().method()
    const path = url.pathname.replace(/^\/api/, '')

    // --- Auth ---
    if (method === 'POST' && path === '/auth/register') {
      const body = JSON.parse(route.request().postData() ?? '{}') as {
        email: string
        password: string
      }
      if (state.users.has(body.email)) {
        await route.fulfill({
          status: 409,
          contentType: 'application/problem+json',
          body: JSON.stringify({
            type: 'errors/already-exists',
            title: 'Email already registered',
            status: 409,
          }),
        })
        return
      }
      const user: RegisteredUser = {
        id: String(state.nextID++),
        email: body.email,
        created_at: new Date().toISOString(),
        last_seen_at: new Date().toISOString(),
      }
      state.users.set(body.email, { password: body.password, user })
      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify(user),
      })
      return
    }

    if (method === 'POST' && path === '/auth/login') {
      const body = JSON.parse(route.request().postData() ?? '{}') as {
        email: string
        password: string
      }
      const acc = state.users.get(body.email)
      if (!acc || acc.password !== body.password) {
        await route.fulfill({
          status: 401,
          contentType: 'application/problem+json',
          body: JSON.stringify({
            type: 'errors/invalid-credentials',
            title: 'Invalid credentials',
            status: 401,
          }),
        })
        return
      }
      const tokens = issueToken(state)
      // Имитируем Set-Cookie (Playwright route.fulfill можно передавать headers).
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        headers: {
          'set-cookie': `rt=${state.currentRefresh}; Path=/api/auth; HttpOnly; SameSite=Strict`,
        },
        body: JSON.stringify(tokens),
      })
      return
    }

    if (method === 'POST' && path === '/auth/logout') {
      state.currentAccess = ''
      state.currentRefresh = ''
      await route.fulfill({
        status: 204,
        headers: {
          'set-cookie': 'rt=; Path=/api/auth; HttpOnly; SameSite=Strict; Max-Age=0',
        },
        body: '',
      })
      return
    }

    if (method === 'POST' && path === '/auth/refresh') {
      // Real refresh uses cookie; here we accept any request as long as state
      // has an active refresh. Body is empty in cookie-mode.
      if (state.currentRefresh === '') {
        await route.fulfill({
          status: 401,
          contentType: 'application/problem+json',
          body: JSON.stringify({ type: 'errors/invalid-token', title: 'invalid refresh', status: 401 }),
        })
        return
      }
      const tokens = issueToken(state)
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        headers: {
          'set-cookie': `rt=${state.currentRefresh}; Path=/api/auth; HttpOnly; SameSite=Strict`,
        },
        body: JSON.stringify(tokens),
      })
      return
    }

    if (method === 'POST' && path === '/stream/ticket') {
      // E2E doesn't actually open EventSource; if a test does, the route handler
      // for /stream is its responsibility. Here we hand out a deterministic stub.
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          ticket: `stub-ticket-${state.nextID++}`,
          expires_at: new Date(Date.now() + 5 * 60_000).toISOString(),
        }),
      })
      return
    }

    // --- Profile ---
    if (method === 'GET' && path === '/me') {
      const acc = pickFirstUser(state)
      if (!acc) {
        await route.fulfill({ status: 401, body: '' })
        return
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(acc.user),
      })
      return
    }

    // --- BMSTU ---
    if (method === 'GET' && path === '/bmstu/status') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: state.bmstuLinked ? 'VALID' : 'NOT_LINKED',
          last_login_at: state.bmstuLinked ? new Date().toISOString() : undefined,
        }),
      })
      return
    }

    if (method === 'POST' && path === '/bmstu/creds') {
      const body = JSON.parse(route.request().postData() ?? '{}') as {
        login: string
        password: string
      }
      // Accept any non-empty creds in the e2e mock — the real test-login is
      // covered by bmstu-svc integration tests.
      if (!body.login || !body.password) {
        await route.fulfill({ status: 400, body: '' })
        return
      }
      state.bmstuLinked = true
      await route.fulfill({ status: 204, body: '' })
      return
    }

    if (method === 'DELETE' && path === '/bmstu/creds') {
      state.bmstuLinked = false
      await route.fulfill({ status: 204, body: '' })
      return
    }

    // --- Filters ---
    if (method === 'GET' && path === '/filters') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ filters: state.filters }),
      })
      return
    }

    if (method === 'POST' && path === '/filters') {
      const body = JSON.parse(route.request().postData() ?? '{}') as Partial<FakeFilter>
      const f: FakeFilter = {
        id: `filter-${state.nextID++}`,
        user_id: '1',
        section: body.section ?? null,
        teacher_uid: body.teacher_uid ?? null,
        day_of_week: body.day_of_week ?? null,
        time_from: body.time_from ?? null,
        time_to: body.time_to ?? null,
        min_rating: body.min_rating ?? null,
        min_vacancy: body.min_vacancy ?? 1,
        enabled: body.enabled ?? true,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
      state.filters.push(f)
      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify(f),
      })
      return
    }

    if (method === 'PATCH' && /^\/filters\/(.+)$/.test(path)) {
      const id = path.split('/').pop() as string
      const body = JSON.parse(route.request().postData() ?? '{}') as Partial<FakeFilter>
      const idx = state.filters.findIndex((f) => f.id === id)
      if (idx < 0) {
        await route.fulfill({ status: 404, body: '' })
        return
      }
      state.filters[idx] = {
        ...state.filters[idx],
        ...body,
        id: state.filters[idx].id,
        updated_at: new Date().toISOString(),
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(state.filters[idx]),
      })
      return
    }

    if (method === 'DELETE' && /^\/filters\/(.+)$/.test(path)) {
      const id = path.split('/').pop() as string
      state.filters = state.filters.filter((f) => f.id !== id)
      await route.fulfill({ status: 204, body: '' })
      return
    }

    // --- Slots & SSE ---
    if (method === 'GET' && path === '/slots') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ slots: [], fetched_at: new Date().toISOString() }),
      })
      return
    }

    // SSE is handled per-test (some tests want to inject events). Default:
    // return an empty open stream that closes quickly so the SPA marks
    // itself "connected" momentarily without further events.
    if (method === 'GET' && path === '/stream') {
      await route.fulfill({
        status: 200,
        headers: {
          'content-type': 'text/event-stream',
          'cache-control': 'no-cache',
          connection: 'keep-alive',
        },
        body: ': initial heartbeat\n\n',
      })
      return
    }

    // Unknown endpoint — return 404 so tests fail loudly on typos.
    await route.fulfill({
      status: 404,
      contentType: 'application/problem+json',
      body: JSON.stringify({ title: 'unhandled in mock', path }),
    })
  })
}

// pickFirstUser returns one of the registered users — used by GET /me when
// it can't tell which user from the bearer token alone (any non-empty
// access token is treated as valid in these mocks).
function pickFirstUser(
  state: BackendState,
): { password: string; user: RegisteredUser } | undefined {
  // Use Array.from rather than iterator destructuring to keep tsc happy
  // under target=ES2022 without --downlevelIteration.
  const all = Array.from(state.users.values())
  return all[0]
}

/**
 * Marker key in `localStorage` that tells {@link seedAuthToken} to stay out
 * of the way. Set by {@link simulateLogout} so a test can navigate to a
 * protected route without the init script re-injecting the access token.
 */
const LOGOUT_MARKER_KEY = 'fizcultor:e2e:logged-out'

/**
 * Seeds an access token in `localStorage` so the SPA's router guard treats
 * the user as authenticated, skipping the UI login flow.
 *
 * Why guarded by a `localStorage` marker: Playwright re-runs every init
 * script on every full navigation (including `page.goto()`). Without the
 * guard, a test that simulates logout (clears the token, navigates to a
 * protected route, expects /login redirect) would have the token re-injected
 * by this very script before the router guard reads it. Tests that want to
 * exercise logout call {@link simulateLogout}, which sets the marker.
 *
 * Why a localStorage marker and not a `window.*` flag: the window object is
 * recreated on every full navigation. localStorage persists across
 * navigations within the same origin, so the marker survives the goto().
 *
 * Refresh-cookie isn't seeded here — it's httpOnly and would require
 * `page.context().addCookies(...)`. Tests that need it use the mocked
 * `/api/auth/login` (which sets the cookie via Set-Cookie).
 */
export async function seedAuthToken(page: Page, token = 'fake-access'): Promise<void> {
  await page.addInitScript(
    ({ key, value, logoutKey }) => {
      // If a previous step in the test simulated logout, do NOT re-seed.
      if (localStorage.getItem(logoutKey) === '1') {
        return
      }
      localStorage.setItem(key, value)
    },
    { key: ACCESS_TOKEN_KEY, value: token, logoutKey: LOGOUT_MARKER_KEY },
  )
}

/**
 * Clears the access token and writes a logout marker into `localStorage`
 * so subsequent navigations don't re-seed it via {@link seedAuthToken}.
 *
 * Idiomatic Playwright pattern for "simulate logout, then navigate to a
 * protected route": without the marker, the init script registered by
 * {@link seedAuthToken} would re-inject the token on every full navigation.
 */
export async function simulateLogout(page: Page): Promise<void> {
  await page.evaluate(
    ({ tokenKey, logoutKey }) => {
      localStorage.setItem(logoutKey, '1')
      localStorage.removeItem(tokenKey)
    },
    { tokenKey: ACCESS_TOKEN_KEY, logoutKey: LOGOUT_MARKER_KEY },
  )
}

/**
 * Installs a controllable shim for `window.EventSource` in the page context.
 *
 * The shim:
 *  - Captures the most recent instance on `window.__sseTest` so tests can
 *    fire deterministic events via `es.dispatchEvent(new MessageEvent(...))`.
 *  - Fires the `open` event on the next tick AND triggers `.onopen` (real
 *    browsers do both; plain EventTarget.dispatchEvent does NOT mirror to
 *    the `.onX` properties, so we mirror manually).
 *  - Mirrors `.onerror` similarly so error simulation works if a test needs it.
 *
 * Must be called BEFORE `page.goto(...)` because the SPA's slots store
 * constructs an EventSource as soon as Dashboard mounts.
 */
export async function installSseShim(page: Page): Promise<void> {
  await page.addInitScript(() => {
    class FakeEventSource extends EventTarget {
      url: string
      readyState = 0
      onopen: ((this: EventSource, ev: Event) => unknown) | null = null
      onerror: ((this: EventSource, ev: Event) => unknown) | null = null
      onmessage: ((this: EventSource, ev: MessageEvent) => unknown) | null = null

      constructor(url: string) {
        super()
        this.url = url
        // Expose the latest instance to the test runner so it can fire
        // arbitrary events into the SPA.
        ;(window as unknown as { __sseTest: FakeEventSource }).__sseTest = this
        // Fire 'open' asynchronously to give the SPA a tick to attach its
        // listeners (mirrors how a real EventSource resolves its handshake).
        setTimeout(() => {
          this.readyState = 1
          const evt = new Event('open')
          this.dispatchEvent(evt)
          // Real EventSource also invokes the `.onopen` property; plain
          // EventTarget does not — mirror it manually.
          if (this.onopen) {
            this.onopen.call(this as unknown as EventSource, evt)
          }
        }, 10)
      }

      override dispatchEvent(event: Event): boolean {
        const handled = super.dispatchEvent(event)
        // Mirror `.onmessage` for default message events; named events bypass
        // this (the SPA uses `addEventListener('new-slot', ...)`).
        if (event.type === 'message' && this.onmessage) {
          this.onmessage.call(this as unknown as EventSource, event as MessageEvent)
        }
        if (event.type === 'error' && this.onerror) {
          this.onerror.call(this as unknown as EventSource, event)
        }
        return handled
      }

      close(): void {
        this.readyState = 2
      }
    }
    const w = window as unknown as { EventSource: typeof EventSource }
    w.EventSource = FakeEventSource as unknown as typeof EventSource
  })
}
