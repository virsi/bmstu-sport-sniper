<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter, RouterLink, RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import ToastContainer from '@/components/ToastContainer.vue'

const auth = useAuthStore()
const router = useRouter()

/**
 * Глобальный bootstrap:
 * 1. Если есть access-токен — пробуем подтянуть профиль (молча; на 401 рефреш отработает сам).
 * 2. Слушаем `auth:logout` (эмит axios-интерсептора) и редиректим на /login.
 */
onMounted(() => {
  if (auth.isAuthenticated && !auth.user) {
    auth.fetchMe().catch(() => {
      // тихий fail — на 401 уже отработает refresh-флоу
    })
  }

  window.addEventListener('auth:logout', () => {
    auth.handleForcedLogout()
    void router.push({ name: 'login' })
  })
})
</script>

<template>
  <div class="flex min-h-full flex-col">
    <header
      v-if="auth.isAuthenticated"
      class="border-b border-gray-200 bg-white"
    >
      <nav class="mx-auto flex max-w-5xl items-center justify-between px-4 py-3">
        <RouterLink
          to="/"
          class="text-lg font-bold text-brand-700"
        >
          fizcultor
        </RouterLink>
        <div class="flex items-center gap-4 text-sm">
          <RouterLink
            to="/"
            class="text-gray-700 hover:text-brand-600"
            active-class="text-brand-600"
          >
            Лента
          </RouterLink>
          <RouterLink
            to="/filters"
            class="text-gray-700 hover:text-brand-600"
            active-class="text-brand-600"
          >
            Фильтры
          </RouterLink>
          <RouterLink
            to="/settings"
            class="text-gray-700 hover:text-brand-600"
            active-class="text-brand-600"
          >
            Настройки
          </RouterLink>
        </div>
      </nav>
    </header>

    <RouterView class="flex-1" />

    <ToastContainer />
  </div>
</template>
