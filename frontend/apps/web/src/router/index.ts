import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { getAccessToken } from '@/api/client'

/**
 * Декларация маршрутов SPA.
 *
 * Конвенции:
 * - Все view-ы лежат в `src/views/<Name>.vue`.
 * - Защищённые маршруты помечены `meta.requiresAuth = true`.
 * - Лоадятся динамически (`() => import(...)`) — это разбивает бандл на чанки.
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/Login.vue'),
    meta: { public: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/views/Register.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    name: 'dashboard',
    component: () => import('@/views/Dashboard.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/Settings.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/filters',
    name: 'filters',
    component: () => import('@/views/Filters.vue'),
    meta: { requiresAuth: true },
  },
  {
    // Catch-all → редирект на дашборд (или /login через guard).
    path: '/:pathMatch(.*)*',
    redirect: { name: 'dashboard' },
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

/**
 * Глобальный guard: проверяет наличие access-токена для защищённых маршрутов.
 *
 * Логика не вызывает backend — на «реальную» валидность токен проверится при первом
 * запросе, и axios-interceptor либо обновит, либо разлогинит. Здесь — only «есть ли токен».
 */
router.beforeEach((to) => {
  const hasToken = Boolean(getAccessToken())
  if (to.meta.requiresAuth && !hasToken) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.meta.public && hasToken && (to.name === 'login' || to.name === 'register')) {
    return { name: 'dashboard' }
  }
  return true
})
