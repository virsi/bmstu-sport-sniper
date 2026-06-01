<script setup lang="ts">
import { computed } from 'vue'
import type { DayOfWeek, Slot } from '@/types/api'

/** Пропсы карточки слота. */
interface Props {
  /**
   * Сам слот.
   *
   * Имя `item` (а не `slot`) — потому что `slot` в Vue 3 зарезервированное слово
   * для slot-директив (vue/no-deprecated-slot-attribute).
   */
  item: Slot
  /** Подсвечивать как «новый» (только что прилетел через SSE). */
  isNew?: boolean
}

const props = withDefaults(defineProps<Props>(), { isNew: false })

/** Человекочитаемый день недели по proto-enum. */
const DAY_LABELS: Record<DayOfWeek, string> = {
  ANY: '—',
  MONDAY: 'Пн',
  TUESDAY: 'Вт',
  WEDNESDAY: 'Ср',
  THURSDAY: 'Чт',
  FRIDAY: 'Пт',
  SATURDAY: 'Сб',
  SUNDAY: 'Вс',
}

const dayLabel = computed<string>(() => {
  const d = props.item.day_of_week
  if (!d || d === 'ANY') {
    return `неделя ${props.item.week}`
  }
  return `${DAY_LABELS[d]}, неделя ${props.item.week}`
})

const vacancyBadgeCls = computed(() => {
  const v = props.item.vacancy
  if (v === 0) {
    return 'bg-red-100 text-red-800'
  }
  if (v <= 2) {
    return 'bg-amber-100 text-amber-800'
  }
  return 'bg-green-100 text-green-800'
})
</script>

<template>
  <article
    :class="[
      'card flex flex-col gap-2',
      props.isNew ? 'ring-2 ring-brand-400' : '',
    ]"
  >
    <header class="flex items-start justify-between gap-2">
      <h3 class="text-base font-semibold text-gray-900">
        {{ item.section ?? 'Без секции' }}
      </h3>
      <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', vacancyBadgeCls]">
        мест: {{ item.vacancy }}
      </span>
    </header>
    <p class="text-sm text-gray-700">
      <span class="text-gray-500">Преподаватель:</span> {{ item.teacher_name }}
      <span
        v-if="item.teacher_rating != null"
        class="ml-1 text-amber-600"
      >
        ★ {{ item.teacher_rating.toFixed(1) }}
      </span>
    </p>
    <p
      v-if="item.place"
      class="text-sm text-gray-600"
    >
      <span class="text-gray-500">Место:</span> {{ item.place }}
    </p>
    <footer class="flex items-center justify-between text-xs text-gray-500">
      <span>{{ item.time }} · {{ dayLabel }}</span>
      <span
        v-if="props.isNew"
        class="rounded bg-brand-100 px-1.5 py-0.5 font-medium text-brand-700"
      >
        новый
      </span>
    </footer>
  </article>
</template>
