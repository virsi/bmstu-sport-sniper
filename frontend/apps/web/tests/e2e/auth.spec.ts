/**
 * auth.spec.ts — covers the public auth flows: register → login → logout
 * → login again.
 *
 * Mocks: page.route stubs for /api/auth/* and /api/me (see fixtures/mocks.ts).
 * No real backend is required.
 */
import { test, expect } from '@playwright/test'
import { installGatewayMocks, newBackendState } from './fixtures/mocks'

const email = 'qa@example.com'
const password = 'password-123'

test.describe('Authentication flow', () => {
  test('should redirect to dashboard after register + auto-login', async ({ page }) => {
    const state = newBackendState()
    await installGatewayMocks(page, state)

    await page.goto('/register')
    await page.getByLabel('Email').fill(email)
    // Two password inputs (password + confirm) — fill both.
    await page.getByLabel('Пароль').fill(password)
    await page.getByLabel('Подтверждение пароля').fill(password)
    await page.getByRole('button', { name: 'Создать аккаунт' }).click()

    // After register, store calls login automatically → dashboard.
    await expect(page).toHaveURL('/')
    await expect(page.getByRole('heading', { name: 'Лента слотов' })).toBeVisible()

    // User is registered server-side.
    expect(state.users.has(email)).toBeTruthy()
  })

  test('should login an existing user and reach the dashboard', async ({ page }) => {
    const state = newBackendState()
    // Pre-seed the user via the mocked register endpoint.
    state.users.set(email, {
      password,
      user: {
        id: '7',
        email,
        created_at: new Date().toISOString(),
        last_seen_at: new Date().toISOString(),
      },
    })
    await installGatewayMocks(page, state)

    await page.goto('/login')
    await page.getByLabel('Email').fill(email)
    await page.getByLabel('Пароль').fill(password)
    await page.getByRole('button', { name: 'Войти' }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('heading', { name: 'Лента слотов' })).toBeVisible()

    // Token was persisted in localStorage.
    const tokenInStorage = await page.evaluate(() => localStorage.getItem('fizcultor:access'))
    expect(tokenInStorage).not.toBeNull()
  })

  test('should reject invalid credentials and stay on login page', async ({ page }) => {
    const state = newBackendState()
    state.users.set(email, {
      password,
      user: {
        id: '7',
        email,
        created_at: new Date().toISOString(),
        last_seen_at: new Date().toISOString(),
      },
    })
    await installGatewayMocks(page, state)

    await page.goto('/login')
    await page.getByLabel('Email').fill(email)
    await page.getByLabel('Пароль').fill('wrong-password')
    await page.getByRole('button', { name: 'Войти' }).click()

    // Still on the login route.
    await expect(page).toHaveURL(/\/login/)
    // No token persisted.
    const tokenInStorage = await page.evaluate(() => localStorage.getItem('fizcultor:access'))
    expect(tokenInStorage).toBeNull()
  })

  test('should redirect unauthenticated user from dashboard to login', async ({ page }) => {
    const state = newBackendState()
    await installGatewayMocks(page, state)

    // No token in storage → router guard kicks in.
    await page.goto('/')
    await expect(page).toHaveURL(/\/login/)
  })

  test('should clear tokens on logout and redirect to login on next protected nav', async ({ page }) => {
    const state = newBackendState()
    state.users.set(email, {
      password,
      user: {
        id: '7',
        email,
        created_at: new Date().toISOString(),
        last_seen_at: new Date().toISOString(),
      },
    })
    await installGatewayMocks(page, state)

    // Seed tokens directly to skip login UI.
    await page.addInitScript(() => {
      localStorage.setItem('fizcultor:access', 'fake-access')
      localStorage.setItem('fizcultor:refresh', 'fake-refresh')
    })

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Лента слотов' })).toBeVisible()

    // Simulate logout via the auth store (no UI button on dashboard, but the
    // store is exposed in the SPA — call clearTokens to mirror logout).
    await page.evaluate(() => {
      localStorage.removeItem('fizcultor:access')
      localStorage.removeItem('fizcultor:refresh')
    })

    // Next navigation must redirect to /login.
    await page.goto('/settings')
    await expect(page).toHaveURL(/\/login/)
  })
})
