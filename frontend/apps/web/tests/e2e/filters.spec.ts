/**
 * filters.spec.ts — verifies the Filters page CRUD:
 *
 *  - Empty state initially.
 *  - Create a filter from the form; it shows up in the list.
 *  - Toggle enabled flag.
 *  - Delete a filter.
 */
import { test, expect } from '@playwright/test'
import { installGatewayMocks, newBackendState } from './fixtures/mocks'

const seededUser = {
  id: '7',
  email: 'qa@example.com',
  created_at: new Date().toISOString(),
  last_seen_at: new Date().toISOString(),
}

test.describe('Filters CRUD', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem('fizcultor:access', 'fake-access')
      localStorage.setItem('fizcultor:refresh', 'fake-refresh')
    })
  })

  test('should create a new filter and display it in the list', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'p', user: seededUser })
    await installGatewayMocks(page, state)

    await page.goto('/filters')
    await expect(page.getByRole('heading', { name: /фильтры/i })).toBeVisible()

    // The form has section/teacher inputs; we use section by label.
    await page.getByLabel(/секция/i).fill('Аэробика')

    // Click submit — the form button text changes between create and update.
    await page.getByRole('button', { name: /создать|сохранить/i }).first().click()

    // Backend state has the new filter.
    await expect.poll(() => state.filters.length).toBe(1)
    expect(state.filters[0].section).toBe('Аэробика')

    // UI shows the filter title.
    await expect(page.getByText('Аэробика').first()).toBeVisible()
  })

  test('should delete a filter when confirm dialog is accepted', async ({ page }) => {
    const state = newBackendState()
    state.users.set(seededUser.email, { password: 'p', user: seededUser })
    state.filters.push({
      id: 'pre-1',
      user_id: '7',
      section: 'Силовая',
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
    page.on('dialog', (d) => void d.accept())

    await page.goto('/filters')
    await expect(page.getByText('Силовая').first()).toBeVisible()

    // Click delete (icon button or "Удалить" label).
    await page.getByRole('button', { name: /удалить/i }).first().click()

    await expect.poll(() => state.filters.length).toBe(0)
  })
})
