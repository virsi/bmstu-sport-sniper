<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Plus,
  Pencil,
  Trash2,
  Power,
  Calendar,
  Clock,
  User,
  Filter as FilterIcon,
  Tag,
  X,
} from 'lucide-vue-next'
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

/** Метки для отображения в списке (короткие). */
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

const editingId = ref<string | null>(null)
const submitting = ref(false)

// Поля формы как локальные ref-ы — позволяют использовать null корректно.
const sectionInput = ref<string>('')
const teacherUidInput = ref<string>('')
const dayInput = ref<DayOfWeek>('ANY')
const timeFromInput = ref<string>('')
const timeToInput = ref<string>('')
const enabledInput = ref<boolean>(true)

onMounted(async () => {
  try {
    await store.fetchAll()
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
})

function resetForm(): void {
  sectionInput.value = ''
  teacherUidInput.value = ''
  dayInput.value = 'ANY'
  timeFromInput.value = ''
  timeToInput.value = ''
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

  const payload: FilterInput = {
    section: nullify(sectionInput.value),
    teacher_uid: nullify(teacherUidInput.value),
    day_of_week: dayInput.value,
    time_from: timeFrom,
    time_to: timeTo,
    min_rating: null, // disabled UI — coming soon
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

/** Чипы-метки для карточки фильтра. */
const enabledCount = computed(() => store.filters.filter((f) => f.enabled).length)
</script>

<template>
  <section class="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-10">
    <header class="mb-8 flex flex-wrap items-end justify-between gap-3">
      <div>
        <span class="badge-violet mb-3">
          <Tag class="h-3 w-3" />
          {{ enabledCount }} активных
        </span>
        <h1 class="h-section">
          Фильтры
        </h1>
        <p class="mt-1 text-sm text-zinc-500">
          Алёрты приходят только по совпадению. Без фильтров — без сообщений.
        </p>
      </div>
    </header>

    <div class="grid gap-6 lg:grid-cols-[1fr_22rem]">
      <!-- ===== Список фильтров (LEFT) ===== -->
      <div>
        <div
          v-if="store.loading"
          class="rounded-2xl border border-zinc-800 bg-zinc-900/40 p-10 text-center text-sm text-zinc-500"
        >
          Загрузка…
        </div>

        <div
          v-else-if="store.filters.length === 0"
          class="rounded-3xl border border-dashed border-zinc-700 bg-zinc-900/30 p-10 text-center backdrop-blur-md"
        >
          <div class="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-zinc-800 bg-zinc-900/70">
            <FilterIcon class="h-7 w-7 text-zinc-500" />
          </div>
          <p class="mb-1 text-base font-semibold text-zinc-200">
            Фильтров пока нет
          </p>
          <p class="mx-auto max-w-sm text-sm text-zinc-500">
            Создай первый — без них алёрты не приходят.
            Форма справа.
          </p>
        </div>

        <ul
          v-else
          class="space-y-3"
        >
          <li
            v-for="f in store.filters"
            :key="f.id"
            :class="[
              'group rounded-2xl border bg-zinc-900/40 p-5 shadow-elevated backdrop-blur-md transition-all duration-200',
              editingId === f.id
                ? 'border-brand-500/40 ring-1 ring-brand-500/40'
                : 'border-zinc-800/80 hover:border-zinc-700',
            ]"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="text-base font-semibold text-zinc-50">
                    {{ filterTitle(f) }}
                  </h3>
                  <span
                    v-if="!f.enabled"
                    class="badge-zinc"
                  >
                    выкл
                  </span>
                  <span
                    v-else
                    class="badge-emerald"
                  >
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                    активен
                  </span>
                </div>

                <!-- Chips -->
                <div class="mt-2.5 flex flex-wrap gap-1.5">
                  <span
                    v-if="f.section"
                    class="inline-flex items-center gap-1 rounded-md bg-zinc-800/70 px-2 py-0.5 text-2xs text-zinc-300"
                  >
                    <Tag class="h-3 w-3 text-zinc-500" />
                    {{ f.section }}
                  </span>
                  <span
                    v-if="f.teacher_uid"
                    class="inline-flex items-center gap-1 rounded-md bg-zinc-800/70 px-2 py-0.5 text-2xs text-zinc-300"
                  >
                    <User class="h-3 w-3 text-zinc-500" />
                    {{ f.teacher_uid }}
                  </span>
                  <span
                    class="inline-flex items-center gap-1 rounded-md bg-zinc-800/70 px-2 py-0.5 text-2xs text-zinc-300"
                  >
                    <Calendar class="h-3 w-3 text-zinc-500" />
                    {{ DAY_LABEL_SHORT[f.day_of_week ?? 'ANY'] }}
                  </span>
                  <span
                    v-if="f.time_from && f.time_to"
                    class="inline-flex items-center gap-1 rounded-md bg-zinc-800/70 px-2 py-0.5 font-mono text-2xs text-zinc-300"
                  >
                    <Clock class="h-3 w-3 text-zinc-500" />
                    {{ f.time_from }}–{{ f.time_to }}
                  </span>
                </div>
              </div>

              <div class="flex shrink-0 gap-1.5">
                <button
                  type="button"
                  :class="[
                    'rounded-lg p-2 transition-all',
                    f.enabled
                      ? 'bg-brand-500/15 text-brand-300 hover:bg-brand-500/25'
                      : 'bg-zinc-800 text-zinc-400 hover:bg-zinc-700 hover:text-zinc-200',
                  ]"
                  :aria-label="f.enabled ? 'Выключить' : 'Включить'"
                  :title="f.enabled ? 'Выключить' : 'Включить'"
                  @click="onToggle(f)"
                >
                  <Power class="h-4 w-4" />
                </button>
                <button
                  type="button"
                  class="rounded-lg bg-zinc-800 p-2 text-zinc-400 transition-all hover:bg-zinc-700 hover:text-zinc-100"
                  aria-label="Изменить"
                  title="Изменить"
                  @click="startEdit(f)"
                >
                  <Pencil class="h-4 w-4" />
                </button>
                <button
                  type="button"
                  class="rounded-lg bg-zinc-800 p-2 text-zinc-400 transition-all hover:bg-rose-500/20 hover:text-rose-300"
                  aria-label="Удалить"
                  title="Удалить"
                  @click="onRemove(f)"
                >
                  <Trash2 class="h-4 w-4" />
                </button>
              </div>
            </div>
          </li>
        </ul>
      </div>

      <!-- ===== Форма (RIGHT, sticky на desktop) ===== -->
      <aside class="lg:sticky lg:top-6 lg:self-start">
        <div class="card-glow">
          <header class="mb-5 flex items-center justify-between">
            <h2 class="flex items-center gap-2 text-lg font-bold text-zinc-50">
              <span
                :class="[
                  'flex h-8 w-8 items-center justify-center rounded-lg',
                  editingId ? 'bg-violet-500/20 text-violet-300' : 'bg-brand-500/20 text-brand-300',
                ]"
              >
                <Pencil
                  v-if="editingId"
                  class="h-4 w-4"
                />
                <Plus
                  v-else
                  class="h-4 w-4"
                />
              </span>
              {{ editingId ? 'Редактирование' : 'Новый фильтр' }}
            </h2>
            <button
              v-if="editingId"
              type="button"
              class="rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-zinc-800 hover:text-zinc-200"
              aria-label="Отменить"
              @click="cancelEdit"
            >
              <X class="h-4 w-4" />
            </button>
          </header>

          <form
            class="space-y-4"
            novalidate
            @submit="onSubmit"
          >
            <BaseInput
              v-model="sectionInput"
              label="Секция"
              placeholder="например, Аэробика"
              hint="Пусто = любая секция"
            />
            <BaseInput
              v-model="teacherUidInput"
              label="UID преподавателя"
              placeholder="uid_42"
              hint="Точное совпадение по BMSTU UID. Пусто = любой"
            />
            <BaseSelect
              v-model="dayInput as string"
              label="День недели"
              :options="DAY_OPTIONS"
            />
            <div class="grid grid-cols-2 gap-3">
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
            </div>

            <BaseInput
              label="Минимальный рейтинг преподавателя"
              type="number"
              :disabled="true"
              placeholder="—"
              hint="Coming soon: filter-svc пока не обогащает рейтингом."
            />

            <!-- Toggle switch для enabled -->
            <label class="flex items-center justify-between gap-3 rounded-xl border border-zinc-800 bg-zinc-900/40 px-4 py-3 cursor-pointer">
              <div>
                <p class="text-sm font-medium text-zinc-200">
                  Активен
                </p>
                <p class="text-xs text-zinc-500">
                  Только активные фильтры присылают алёрты
                </p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="enabledInput"
                :class="[
                  'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950',
                  enabledInput ? 'bg-brand-500' : 'bg-zinc-700',
                ]"
                @click="enabledInput = !enabledInput"
              >
                <span
                  :class="[
                    'inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform duration-200',
                    enabledInput ? 'translate-x-6' : 'translate-x-1',
                  ]"
                />
              </button>
              <input
                v-model="enabledInput"
                type="checkbox"
                class="sr-only"
              >
            </label>

            <div class="flex gap-2 pt-1">
              <BaseButton
                type="submit"
                :loading="submitting"
                block
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
        </div>
      </aside>
    </div>
  </section>
</template>
