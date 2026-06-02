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
import { installGatewayMocks, newBackendState, seedAuthToken } from './fixtures/mocks'

const seededUser = {
  id: '7',
  email: 'qa@example.com',
  created_at: new Date().toISOString(),
  last_seen_at: new Date().toISOString(),
}

test.describe('BMSTU credentials linking', () => {
  test.beforeEach(async ({ page }) => {
    // Seed an access token so router guards let us into Settings.
    await seedAuthToken(page)
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

    // The delete-creds button shows up only when `bmstu.flags.linked` is true
    // (Settings.vue conditional `v-if`). Label is "Удалить" (no "креды" suffix);
    // exact-match regex avoids matching any future "Удалить *" labels nearby.
    await page.getByRole('button', { name: /^удалить$/i }).click()

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

  test('should show read-only summary when linked, then toggle edit form', async ({
    page,
  }) => {
    // Pre-condition: креды уже сохранены — UI должен показать summary,
    // а не пустую форму (UX-regression от ходьбы между страницами).
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'password-123', user: seededUser })
    state.bmstuLinked = true
    state.bmstuHealthGroup = 'PREPARATORY'
    await installGatewayMocks(page, state)

    await page.goto('/settings')

    // Summary visible: human-readable health group label + status badge.
    await expect(page.getByText(/привязан, активен/i)).toBeVisible()
    await expect(page.getByText(/Подготовительная/i)).toBeVisible()
    // Login/password inputs must NOT be in DOM in read-only mode — иначе
    // регрессия «пустая форма после возврата» вернётся.
    await expect(page.getByLabel(/логин/i)).toHaveCount(0)
    await expect(page.getByLabel(/пароль/i)).toHaveCount(0)

    // Click «Изменить креды» — форма должна появиться, login/password пустые.
    await page.getByRole('button', { name: /изменить креды/i }).click()
    await expect(page.getByLabel(/логин/i)).toBeVisible()
    await expect(page.getByLabel(/логин/i)).toHaveValue('')
    await expect(page.getByLabel(/пароль/i)).toHaveValue('')

    // «Отмена» возвращает в read-only.
    await page.getByRole('button', { name: /отмена/i }).click()
    await expect(page.getByLabel(/логин/i)).toHaveCount(0)
    await expect(page.getByText(/Подготовительная/i)).toBeVisible()
  })
})
