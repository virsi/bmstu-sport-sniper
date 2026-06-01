<script setup lang="ts">
import { onMounted, onBeforeUnmount, computed } from 'vue'
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

const connectionLabel = computed(() =>
  slotsStore.connected ? 'Live-канал активен' : 'Соединение отсутствует',
)
const connectionDotCls = computed(() =>
  slotsStore.connected ? 'bg-green-500' : 'bg-gray-400',
)

const hasNoFilters = computed(() => filtersStore.filters.length === 0)
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
  <section class="mx-auto max-w-5xl px-4 py-6">
    <header class="mb-6 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-bold text-gray-900">
        Лента слотов
      </h1>
      <div class="flex items-center gap-3 text-sm">
        <span class="flex items-center gap-2 text-gray-600">
          <span
            :class="['h-2 w-2 rounded-full', connectionDotCls]"
            aria-hidden="true"
          />
          <span>{{ connectionLabel }}</span>
        </span>
        <BaseButton
          variant="secondary"
          :loading="slotsStore.loading"
          @click="refresh"
        >
          Обновить
        </BaseButton>
      </div>
    </header>

    <p
      v-if="fetchedAtLabel"
      class="mb-3 text-xs text-gray-500"
    >
      Снимок от {{ fetchedAtLabel }}
    </p>

    <div
      v-if="slotsStore.loading"
      class="flex justify-center py-12 text-brand-600"
    >
      <Spinner />
    </div>

    <div
      v-else-if="hasNoFilters"
      class="rounded-lg border border-dashed border-brand-300 bg-brand-50 p-8 text-center"
    >
      <p class="mb-3 text-sm text-brand-900">
        У тебя пока нет фильтров — без них алёрты не будут приходить.
      </p>
      <RouterLink
        to="/filters"
        class="inline-flex items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
      >
        Настроить фильтры
      </RouterLink>
    </div>

    <div
      v-else-if="slotsStore.slots.length === 0"
      class="rounded-lg border border-dashed border-gray-300 bg-white p-8 text-center"
    >
      <p class="text-sm text-gray-600">
        Пока нет слотов. Они появятся здесь, как только BMSTU-расписание изменится.
      </p>
    </div>

    <div
      v-else
      class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3"
    >
      <SlotCard
        v-for="slot in slotsStore.slots"
        :key="slot.id"
        :item="slot"
      />
    </div>
  </section>
</template>
