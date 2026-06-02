<script setup lang="ts">
import { onMounted, onBeforeUnmount, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { RefreshCw, Radio, Filter, ListChecks, Sparkles, Calendar } from 'lucide-vue-next'
import { useSlotsStore } from '@/stores/slots'
import { useFiltersStore } from '@/stores/filters'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import SlotCard from '@/components/SlotCard.vue'
import Spinner from '@/components/Spinner.vue'
import BaseButton from '@/components/BaseButton.vue'

const slotsStore = useSlotsStore()
const filtersStore = useFiltersStore()
const { error: toastError } = useToast()

onMounted(async () => {
  try {
    await Promise.all([slotsStore.fetchInitial(), filtersStore.fetchAll()])
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
  slotsStore.subscribe()
})

onBeforeUnmount(() => {
  slotsStore.unsubscribe()
})

/** Связано: текст SSE-индикатора + цвет точки. */
const connectionLabel = computed(() =>
  slotsStore.connected ? 'Live-канал активен' : 'Соединение отсутствует',
)
const connectionActive = computed(() => slotsStore.connected)

/** Подсчёт активных фильтров — для hero-stat. */
const enabledFiltersCount = computed(
  () => filtersStore.filters.filter((f) => f.enabled).length,
)

const hasNoFilters = computed(() => filtersStore.filters.length === 0)

/** Время последнего снимка (для подписи). */
const fetchedAtLabel = computed(() => {
  if (!slotsStore.fetchedAt) {
    return ''
  }
  return new Date(slotsStore.fetchedAt).toLocaleTimeString('ru-RU')
})

async function refresh(): Promise<void> {
  try {
    await slotsStore.fetchInitial()
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
}
</script>

<template>
  <section class="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-10">
    <!-- ===== Header ===== -->
    <header class="mb-8 flex flex-wrap items-end justify-between gap-4">
      <div>
        <span class="badge-emerald mb-3">
          <Sparkles class="h-3 w-3" />
          Live
        </span>
        <h1 class="h-section">
          Лента слотов
        </h1>
        <p
          v-if="fetchedAtLabel"
          class="mt-1 font-mono text-xs text-zinc-500"
        >
          снимок · {{ fetchedAtLabel }}
        </p>
      </div>
      <BaseButton
        variant="secondary"
        size="sm"
        :loading="slotsStore.loading"
        @click="refresh"
      >
        <template #icon-left>
          <RefreshCw class="h-3.5 w-3.5" />
        </template>
        Обновить
      </BaseButton>
    </header>

    <!-- ===== Hero stats ===== -->
    <div class="mb-8 grid gap-3 sm:grid-cols-3">
      <!-- Live status -->
      <div
        :class="[
          'card flex items-center justify-between gap-3 transition-colors',
          connectionActive && 'border-brand-500/40',
        ]"
      >
        <div>
          <p class="text-2xs uppercase tracking-wider text-zinc-500">
            SSE-канал
          </p>
          <p
            :class="[
              'mt-1 text-base font-semibold',
              connectionActive ? 'text-brand-300' : 'text-zinc-400',
            ]"
          >
            {{ connectionLabel }}
          </p>
        </div>
        <span class="relative flex h-10 w-10 items-center justify-center rounded-xl bg-zinc-900/70">
          <Radio
            :class="[
              'h-5 w-5',
              connectionActive ? 'text-brand-400' : 'text-zinc-600',
            ]"
          />
          <span
            v-if="connectionActive"
            class="absolute -top-0.5 -right-0.5 h-2 w-2 animate-pulse-glow rounded-full bg-brand-400"
            aria-hidden="true"
          />
        </span>
      </div>

      <!-- Active filters -->
      <div class="card flex items-center justify-between gap-3">
        <div>
          <p class="text-2xs uppercase tracking-wider text-zinc-500">
            Активных фильтров
          </p>
          <p class="mt-1 text-2xl font-bold tabular text-zinc-50">
            {{ enabledFiltersCount }}
            <span class="font-mono text-xs font-normal text-zinc-500">
              из {{ filtersStore.filters.length }}
            </span>
          </p>
        </div>
        <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-zinc-900/70">
          <Filter class="h-5 w-5 text-violet-400" />
        </span>
      </div>

      <!-- Slots tracked -->
      <div class="card flex items-center justify-between gap-3">
        <div>
          <p class="text-2xs uppercase tracking-wider text-zinc-500">
            Слотов в ленте
          </p>
          <p class="mt-1 text-2xl font-bold tabular text-zinc-50">
            {{ slotsStore.slots.length }}
          </p>
        </div>
        <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-zinc-900/70">
          <ListChecks class="h-5 w-5 text-emerald-400" />
        </span>
      </div>
    </div>

    <!-- ===== Loading ===== -->
    <div
      v-if="slotsStore.loading"
      class="flex justify-center py-16 text-brand-400"
    >
      <Spinner size="lg" />
    </div>

    <!-- ===== Empty state: no filters ===== -->
    <div
      v-else-if="hasNoFilters"
      class="relative overflow-hidden rounded-3xl border border-brand-500/30 bg-zinc-900/40 p-10 text-center shadow-elevated backdrop-blur-md"
    >
      <div
        class="pointer-events-none absolute inset-0 bg-gradient-hero opacity-60"
        aria-hidden="true"
      />
      <div class="relative">
        <div class="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-brand shadow-glow">
          <Filter class="h-8 w-8 text-white" />
        </div>
        <h2 class="mb-2 text-xl font-bold text-zinc-50">
          Создай первый фильтр
        </h2>
        <p class="mx-auto mb-6 max-w-md text-sm text-zinc-400">
          Без фильтров алёрты не приходят. Укажи секцию, день недели и желаемое
          время — мы пришлём слот через
          <span class="font-semibold text-brand-300">3 секунды</span>
          после его появления.
        </p>
        <RouterLink
          to="/filters"
          class="btn-primary inline-flex"
        >
          <Filter class="h-4 w-4" />
          Настроить фильтры
        </RouterLink>
      </div>
    </div>

    <!-- ===== Empty state: filters set but no slots yet ===== -->
    <div
      v-else-if="slotsStore.slots.length === 0"
      class="rounded-3xl border border-dashed border-zinc-700 bg-zinc-900/30 p-10 text-center backdrop-blur-md"
    >
      <div class="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-zinc-800 bg-zinc-900/70">
        <Calendar class="h-7 w-7 text-zinc-500" />
      </div>
      <p class="mb-1 text-base font-semibold text-zinc-200">
        Пока тишина
      </p>
      <p class="mx-auto max-w-sm text-sm text-zinc-500">
        Слоты появятся здесь, как только BMSTU-расписание изменится.
        Можешь оставить эту вкладку открытой — обновится автоматически.
      </p>
    </div>

    <!-- ===== Slots grid ===== -->
    <div
      v-else
      class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"
    >
      <SlotCard
        v-for="slot in slotsStore.slots"
        :key="slot.id"
        :item="slot"
      />
    </div>
  </section>
</template>
