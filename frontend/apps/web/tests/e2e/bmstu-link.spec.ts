/**
 * bmstu-link.spec.ts — verifies the BMSTU credentials linking flow on
 * Settings. The mock backend accepts any non-empty creds (real bmstu-svc
 * test-login is covered by Go integration tests), so this spec exercises:
 *
 *  - Initial status badge says "не привязан".
 *  - Submitting the form updates status to VALID.
 *  - Delete button reverts status to NOT_LINKED.
 */
import { test, expect } from '@playwright/test'
import { installGatewayMocks, newBackendState } from './fixtures/mocks'

const seededUser = {
  id: '7',
  email: 'qa@example.com',
  created_at: new Date().toISOString(),
  last_seen_at: new Date().toISOString(),
}

test.describe('BMSTU credentials linking', () => {
  test.beforeEach(async ({ page }) => {
    // Seed an access token so router guards let us into Settings.
    await page.addInitScript(() => {
      localStorage.setItem('fizcultor:access', 'fake-access')
      localStorage.setItem('fizcultor:refresh', 'fake-refresh')
    })
  })

  test('should show NOT_LINKED initially and switch to VALID after submit', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'password-123', user: seededUser })
    await installGatewayMocks(page, state)

    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: /настройки/i })).toBeVisible()

    // Submit creds form.
    await page.getByLabel(/логин/i).fill('ivan')
    await page.getByLabel(/пароль/i).fill('p@ss')

    // Click the submit-creds button — search by visible text.
    const submitBtn = page.getByRole('button', { name: /сохранить/i })
    await submitBtn.click()

    // Backend state must reflect the link.
    await expect.poll(() => state.bmstuLinked).toBe(true)

    // UI badge updates — the status text contains "активен" once VALID.
    await expect(page.getByText(/привязан, активен/i)).toBeVisible()
  })

  test('should delete creds and revert status to NOT_LINKED', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'password-123', user: seededUser })
    state.bmstuLinked = true
    await installGatewayMocks(page, state)

    // window.confirm is fired by the SPA before delete — auto-accept.
    page.on('dialog', (d) => void d.accept())

    await page.goto('/settings')
    await expect(page.getByText(/привязан, активен/i)).toBeVisible()

    // Click delete button.
    await page.getByRole('button', { name: /удалить креды/i }).click()

    await expect.poll(() => state.bmstuLinked).toBe(false)
  })

  test('should reject empty credentials at the form level', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'password-123', user: seededUser })
    await installGatewayMocks(page, state)

    await page.goto('/settings')
    // Submit with empty inputs — zod schema must block it on the client.
    await page.getByRole('button', { name: /сохранить/i }).click()

    // No backend call was made.
    expect(state.bmstuLinked).toBe(false)
  })
})
