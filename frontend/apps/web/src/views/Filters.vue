<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useFiltersStore } from '@/stores/filters'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import type { DayOfWeek, Filter, FilterInput } from '@/types/api'
import BaseButton from '@/components/BaseButton.vue'
import BaseInput from '@/components/BaseInput.vue'
import BaseSelect, { type SelectOption } from '@/components/BaseSelect.vue'

const store = useFiltersStore()
const { success, error: toastError } = useToast()

/** Опции селекта «День недели». */
const DAY_OPTIONS: SelectOption[] = [
  { value: 'ANY', label: 'Любой день' },
  { value: 'MONDAY', label: 'Понедельник' },
  { value: 'TUESDAY', label: 'Вторник' },
  { value: 'WEDNESDAY', label: 'Среда' },
  { value: 'THURSDAY', label: 'Четверг' },
  { value: 'FRIDAY', label: 'Пятница' },
  { value: 'SATURDAY', label: 'Суббота' },
  { value: 'SUNDAY', label: 'Воскресенье' },
]

/** Метки для отображения в списке. */
const DAY_LABEL_SHORT: Record<DayOfWeek, string> = {
  ANY: 'любой',
  MONDAY: 'Пн',
  TUESDAY: 'Вт',
  WEDNESDAY: 'Ср',
  THURSDAY: 'Чт',
  FRIDAY: 'Пт',
  SATURDAY: 'Сб',
  SUNDAY: 'Вс',
}

/** Дефолт пустой формы. */
function emptyInput(): FilterInput {
  return {
    section: null,
    teacher_uid: null,
    day_of_week: 'ANY',
    time_from: null,
    time_to: null,
    min_rating: null,
    min_vacancy: 1,
    enabled: true,
  }
}

const draft = ref<FilterInput>(emptyInput())
const editingId = ref<string | null>(null)
const submitting = ref(false)

// Поля формы как локальные ref-ы — позволяют использовать null корректно.
const sectionInput = ref<string>('')
const teacherUidInput = ref<string>('')
const dayInput = ref<DayOfWeek>('ANY')
const timeFromInput = ref<string>('')
const timeToInput = ref<string>('')
const minVacancyInput = ref<number | null>(1)
const enabledInput = ref<boolean>(true)

onMounted(async () => {
  try {
    await store.fetchAll()
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
})

function resetForm(): void {
  draft.value = emptyInput()
  sectionInput.value = ''
  teacherUidInput.value = ''
  dayInput.value = 'ANY'
  timeFromInput.value = ''
  timeToInput.value = ''
  minVacancyInput.value = 1
  enabledInput.value = true
  editingId.value = null
}

function startEdit(f: Filter): void {
  editingId.value = f.id
  sectionInput.value = f.section ?? ''
  teacherUidInput.value = f.teacher_uid ?? ''
  dayInput.value = f.day_of_week ?? 'ANY'
  timeFromInput.value = f.time_from ?? ''
  timeToInput.value = f.time_to ?? ''
  minVacancyInput.value = f.min_vacancy
  enabledInput.value = f.enabled
}

function cancelEdit(): void {
  resetForm()
}

/** Преобразует пустую строку в null (для опциональных string-полей). */
function nullify(s: string): string | null {
  return s.trim() === '' ? null : s.trim()
}

/** Валидирует, что time_to >= time_from если оба заданы. */
function validateTimeRange(from: string | null, to: string | null): string | null {
  if (!from || !to) {
    return null
  }
  if (from > to) {
    return 'Время «до» должно быть не раньше «с»'
  }
  return null
}

async function onSubmit(e: Event): Promise<void> {
  e.preventDefault()
  const timeFrom = nullify(timeFromInput.value)
  const timeTo = nullify(timeToInput.value)
  const timeErr = validateTimeRange(timeFrom, timeTo)
  if (timeErr) {
    toastError(timeErr)
    return
  }
  if (minVacancyInput.value === null || minVacancyInput.value < 1) {
    toastError('Минимум свободных мест — 1')
    return
  }

  const payload: FilterInput = {
    section: nullify(sectionInput.value),
    teacher_uid: nullify(teacherUidInput.value),
    day_of_week: dayInput.value,
    time_from: timeFrom,
    time_to: timeTo,
    min_rating: null, // disabled UI — coming soon
    min_vacancy: minVacancyInput.value,
    enabled: enabledInput.value,
  }

  submitting.value = true
  try {
    if (editingId.value) {
      await store.update(editingId.value, payload)
      success('Фильтр обновлён')
    } else {
      await store.create(payload)
      success('Фильтр создан')
    }
    resetForm()
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    submitting.value = false
  }
}

async function onRemove(f: Filter): Promise<void> {
  const label = f.section ?? f.teacher_uid ?? `id=${f.id.slice(0, 8)}`
  if (!window.confirm(`Удалить фильтр «${label}»?`)) {
    return
  }
  try {
    await store.remove(f.id)
    success('Фильтр удалён')
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
}

async function onToggle(f: Filter): Promise<void> {
  try {
    await store.toggleEnabled(f.id, !f.enabled)
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
}

/** Подпись фильтра в списке. */
function filterTitle(f: Filter): string {
  return f.section ?? f.teacher_uid ?? 'Любой слот'
}
</script>

<template>
  <section class="mx-auto max-w-3xl px-4 py-6">
    <h1 class="mb-6 text-2xl font-bold text-gray-900">
      Фильтры
    </h1>

    <article class="card mb-6">
      <h2 class="mb-3 text-lg font-semibold">
        {{ editingId ? 'Редактирование' : 'Новый фильтр' }}
      </h2>
      <form
        class="grid gap-4 sm:grid-cols-2"
        novalidate
        @submit="onSubmit"
      >
        <div class="sm:col-span-2">
          <BaseInput
            v-model="sectionInput"
            label="Секция"
            placeholder="например, Аэробика"
            hint="Пусто = любая секция"
          />
        </div>
        <div class="sm:col-span-2">
          <BaseInput
            v-model="teacherUidInput"
            label="UID преподавателя"
            placeholder="uid_42"
            hint="Точное совпадение по BMSTU UID. Пусто = любой"
          />
        </div>
        <BaseSelect
          v-model="dayInput as string"
          label="День недели"
          :options="DAY_OPTIONS"
        />
        <BaseInput
          v-model.number="minVacancyInput"
          label="Минимум свободных мест"
          type="number"
          min="1"
        />
        <BaseInput
          v-model="timeFromInput"
          label="Время с"
          type="time"
          hint="Пусто = без ограничения"
        />
        <BaseInput
          v-model="timeToInput"
          label="Время до"
          type="time"
          hint="Пусто = без ограничения"
        />
        <div class="sm:col-span-2">
          <BaseInput
            label="Минимальный рейтинг преподавателя"
            type="number"
            :disabled="true"
            placeholder="—"
            hint="Coming soon: filter-svc пока не обогащает рейтингом."
          />
        </div>
        <label class="flex items-center gap-2 text-sm text-gray-700 sm:col-span-2">
          <input
            v-model="enabledInput"
            type="checkbox"
            class="h-4 w-4"
          >
          Активен
        </label>
        <div class="flex gap-2 sm:col-span-2">
          <BaseButton
            type="submit"
            :loading="submitting"
          >
            {{ editingId ? 'Сохранить' : 'Создать' }}
          </BaseButton>
          <BaseButton
            v-if="editingId"
            type="button"
            variant="secondary"
            @click="cancelEdit"
          >
            Отмена
          </BaseButton>
        </div>
      </form>
    </article>

    <div
      v-if="store.loading"
      class="text-center text-sm text-gray-500"
    >
      Загрузка…
    </div>

    <div
      v-else-if="store.filters.length === 0"
      class="rounded-lg border border-dashed border-gray-300 bg-white p-6 text-center text-sm text-gray-600"
    >
      Фильтров пока нет — создай первый. Без фильтров алёрты не приходят.
    </div>

    <ul
      v-else
      class="space-y-2"
    >
      <li
        v-for="f in store.filters"
        :key="f.id"
        class="card flex items-start justify-between gap-3"
      >
        <div class="flex-1">
          <h3 class="text-base font-semibold">
            {{ filterTitle(f) }}
            <span
              v-if="!f.enabled"
              class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-xs font-medium text-gray-600"
            >
              выкл
            </span>
          </h3>
          <p class="mt-1 text-xs text-gray-600">
            <span v-if="f.section">Секция: {{ f.section }} · </span>
            <span v-if="f.teacher_uid">UID препода: {{ f.teacher_uid }} · </span>
            День: {{ DAY_LABEL_SHORT[f.day_of_week ?? 'ANY'] }} ·
            <span v-if="f.time_from && f.time_to">
              {{ f.time_from }}–{{ f.time_to }} ·
            </span>
            мин. мест: {{ f.min_vacancy }}
          </p>
        </div>
        <div class="flex shrink-0 flex-col gap-2 sm:flex-row">
          <BaseButton
            variant="secondary"
            @click="onToggle(f)"
          >
            {{ f.enabled ? 'Выкл' : 'Вкл' }}
          </BaseButton>
          <BaseButton
            variant="secondary"
            @click="startEdit(f)"
          >
            Изменить
          </BaseButton>
          <BaseButton
            variant="danger"
            @click="onRemove(f)"
          >
            Удалить
          </BaseButton>
        </div>
      </li>
    </ul>
  </section>
</template>
