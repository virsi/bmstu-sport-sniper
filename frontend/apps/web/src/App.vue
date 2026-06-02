<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter, RouterView } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import AppShell from '@/components/AppShell.vue'
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
  <div class="relative isolate flex min-h-full flex-col">
    <!-- Aутентифицированный раздел рендерится в AppShell с sidebar / bottom-bar. -->
    <AppShell v-if="auth.isAuthenticated" />
    <!-- Публичные страницы (/login, /register) — без shell, в полный экран. -->
    <RouterView v-else />

    <ToastContainer />
  </div>
</template>
