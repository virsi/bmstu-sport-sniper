<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import {
  LayoutDashboard,
  ListFilter,
  Settings as SettingsIcon,
  LogOut,
  Zap,
} from 'lucide-vue-next'
import { useAuthStore } from '@/stores/auth'

/**
 * Корневой layout аутентифицированного раздела.
 *
 * Структура:
 * - **Desktop ≥ md (768px)**: фиксированный sidebar слева (логотип / nav / user-action),
 *   контент справа в полной ширине.
 * - **Mobile < md**: sticky top bar (логотип + email) + bottom tab-bar для навигации.
 *   Bottom-bar — 4 пункта (Dashboard / Filters / Settings / Logout).
 *
 * Поведение:
 * - Активный route подсвечивается брендовым цветом + emerald dot.
 * - Sidebar навигация с smooth transitions.
 */
const auth = useAuthStore()
const route = useRoute()

interface NavItem {
  name: string
  to: string
  label: string
  icon: typeof LayoutDashboard
}

const navItems: NavItem[] = [
  { name: 'dashboard', to: '/', label: 'Лента', icon: LayoutDashboard },
  { name: 'filters', to: '/filters', label: 'Фильтры', icon: ListFilter },
  { name: 'settings', to: '/settings', label: 'Настройки', icon: SettingsIcon },
]

/** Возвращает email пользователя или fallback. */
const userEmail = computed(() => auth.user?.email ?? 'guest')

/** Подсветка активного пункта (точное совпадение route.name). */
function isActive(item: NavItem): boolean {
  return route.name === item.name
}

async function onLogout(): Promise<void> {
  await auth.logout()
}
</script>

<template>
  <div class="flex min-h-full">
    <!-- ===== Desktop sidebar ===== -->
    <aside
      class="sticky top-0 hidden h-screen w-60 shrink-0 flex-col border-r border-zinc-800/80 bg-zinc-950/60 px-4 py-6 backdrop-blur-xl md:flex"
      aria-label="Главная навигация"
    >
      <!-- Лого -->
      <RouterLink
        to="/"
        class="mb-8 flex items-center gap-2.5 px-2"
      >
        <span class="relative flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-brand shadow-glow">
          <Zap class="h-5 w-5 text-white" />
        </span>
        <div class="leading-tight">
          <p class="text-sm font-bold tracking-tight text-zinc-50">
            fizcultor
          </p>
          <p class="text-2xs uppercase tracking-wider text-zinc-500">
            BMSTU sniper
          </p>
        </div>
      </RouterLink>

      <!-- Nav -->
      <nav class="flex flex-1 flex-col gap-1">
        <RouterLink
          v-for="item in navItems"
          :key="item.name"
          :to="item.to"
          :class="[
            'group flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition-all duration-200',
            isActive(item)
              ? 'bg-brand-500/10 text-brand-300 ring-1 ring-inset ring-brand-500/30'
              : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100',
          ]"
        >
          <component
            :is="item.icon"
            :class="[
              'h-4 w-4 transition-colors',
              isActive(item) ? 'text-brand-300' : 'text-zinc-500 group-hover:text-zinc-300',
            ]"
          />
          <span>{{ item.label }}</span>
          <span
            v-if="isActive(item)"
            class="ml-auto h-1.5 w-1.5 rounded-full bg-brand-400"
            aria-hidden="true"
          />
        </RouterLink>
      </nav>

      <!-- User / logout -->
      <div class="mt-4 border-t border-zinc-800/80 pt-4">
        <div class="mb-3 px-3">
          <p class="text-2xs uppercase tracking-wider text-zinc-500">
            Аккаунт
          </p>
          <p class="mt-0.5 truncate font-mono text-xs text-zinc-300">
            {{ userEmail }}
          </p>
        </div>
        <button
          type="button"
          class="group flex w-full items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium text-zinc-400 transition-colors hover:bg-rose-500/10 hover:text-rose-300"
          @click="onLogout"
        >
          <LogOut class="h-4 w-4 text-zinc-500 transition-colors group-hover:text-rose-400" />
          Выйти
        </button>
      </div>
    </aside>

    <!-- ===== Main column ===== -->
    <div class="flex min-w-0 flex-1 flex-col">
      <!-- Mobile top bar -->
      <header
        class="sticky top-0 z-30 flex items-center justify-between border-b border-zinc-800/80 bg-zinc-950/80 px-4 py-3 backdrop-blur-xl md:hidden"
      >
        <RouterLink
          to="/"
          class="flex items-center gap-2"
        >
          <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-brand shadow-glow">
            <Zap class="h-4 w-4 text-white" />
          </span>
          <span class="text-sm font-bold text-zinc-50">fizcultor</span>
        </RouterLink>
        <span class="truncate font-mono text-2xs text-zinc-500">
          {{ userEmail }}
        </span>
      </header>

      <!-- Page content -->
      <main class="flex-1 pb-20 md:pb-0">
        <RouterView v-slot="{ Component }">
          <Transition
            enter-active-class="transition-all duration-300 ease-out"
            enter-from-class="translate-y-1 opacity-0"
            enter-to-class="translate-y-0 opacity-100"
            mode="out-in"
          >
            <component :is="Component" />
          </Transition>
        </RouterView>
      </main>

      <!-- Mobile bottom nav -->
      <nav
        class="fixed inset-x-0 bottom-0 z-30 flex border-t border-zinc-800/80 bg-zinc-950/90 backdrop-blur-xl md:hidden"
        aria-label="Главная навигация"
      >
        <RouterLink
          v-for="item in navItems"
          :key="item.name"
          :to="item.to"
          :class="[
            'flex flex-1 flex-col items-center justify-center gap-1 py-2.5 text-2xs font-medium transition-colors',
            isActive(item) ? 'text-brand-300' : 'text-zinc-400 active:bg-zinc-800/50',
          ]"
        >
          <component
            :is="item.icon"
            class="h-5 w-5"
          />
          <span>{{ item.label }}</span>
        </RouterLink>
        <button
          type="button"
          class="flex flex-1 flex-col items-center justify-center gap-1 py-2.5 text-2xs font-medium text-zinc-400 transition-colors active:bg-zinc-800/50"
          @click="onLogout"
        >
          <LogOut class="h-5 w-5" />
          <span>Выйти</span>
        </button>
      </nav>
    </div>
  </div>
</template>
