<script setup lang="ts">
import { computed } from 'vue'
import { MapPin, Clock, User, Star, Sparkles } from 'lucide-vue-next'
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
  return `${DAY_LABELS[d]} · неделя ${props.item.week}`
})

/**
 * Возвращает класс для бейджа вакансий: красный (нет мест) / янтарный (1–2) /
 * emerald (норм). Цвета сбалансированы под dark-тему.
 */
const vacancyBadgeCls = computed(() => {
  const v = props.item.vacancy
  if (v === 0) {
    return 'badge-rose'
  }
  if (v <= 2) {
    return 'badge-amber'
  }
  return 'badge-emerald'
})

/** Численный рейтинг → строка с 1 знаком после запятой. */
const ratingLabel = computed(() =>
  props.item.teacher_rating != null ? props.item.teacher_rating.toFixed(1) : null,
)
</script>

<template>
  <article
    :class="[
      'group relative flex flex-col gap-3 overflow-hidden rounded-2xl border bg-zinc-900/40 p-5 shadow-elevated backdrop-blur-md',
      'transition-all duration-300 ease-out hover:-translate-y-0.5 hover:border-brand-500/40 hover:shadow-glow',
      props.isNew ? 'border-brand-500/60 shadow-glow animate-fade-up' : 'border-zinc-800/80',
    ]"
  >
    <!-- Декоративный градиент top-right, проявляется на hover. -->
    <div
      class="pointer-events-none absolute -right-12 -top-12 h-32 w-32 rounded-full bg-brand-500/10 opacity-0 blur-2xl transition-opacity duration-500 group-hover:opacity-100"
      aria-hidden="true"
    />

    <header class="flex items-start justify-between gap-3">
      <div class="min-w-0 flex-1">
        <h3 class="truncate text-base font-semibold text-zinc-50">
          {{ item.section ?? 'Без секции' }}
        </h3>
        <p class="mt-0.5 flex items-center gap-1.5 text-2xs uppercase tracking-wide text-zinc-500">
          <Clock class="h-3 w-3" />
          <span class="tabular">{{ item.time }}</span>
          <span class="text-zinc-700">·</span>
          <span>{{ dayLabel }}</span>
        </p>
      </div>

      <span :class="vacancyBadgeCls">
        <span class="tabular">{{ item.vacancy }}</span>
        <span class="opacity-70">мест</span>
      </span>
    </header>

    <div class="space-y-1.5 text-sm">
      <p class="flex items-start gap-2 text-zinc-200">
        <User class="mt-0.5 h-4 w-4 shrink-0 text-zinc-500" />
        <span class="min-w-0 flex-1 truncate">{{ item.teacher_name }}</span>
        <span
          v-if="ratingLabel"
          class="flex items-center gap-0.5 text-amber-300 tabular"
        >
          <Star class="h-3.5 w-3.5 fill-current" />
          {{ ratingLabel }}
        </span>
      </p>
      <p
        v-if="item.place"
        class="flex items-center gap-2 text-zinc-400"
      >
        <MapPin class="h-4 w-4 shrink-0 text-zinc-600" />
        <span class="truncate">{{ item.place }}</span>
      </p>
    </div>

    <footer
      v-if="props.isNew"
      class="flex items-center justify-between pt-1"
    >
      <span class="badge-violet">
        <Sparkles class="h-3 w-3" />
        новый
      </span>
    </footer>
  </article>
</template>
