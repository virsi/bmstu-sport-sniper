<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import {
  Send,
  GraduationCap,
  ShieldCheck,
  RefreshCw,
  User as UserIcon,
  LogOut,
  CheckCircle2,
  AlertTriangle,
  Clock,
  Lock,
  ExternalLink,
  Activity,
  Heart,
  Stethoscope,
  Accessibility,
  Pencil,
  Trash2,
  X as XIcon,
} from 'lucide-vue-next'
import { useBmstuStore } from '@/stores/bmstu'
import { useAuthStore } from '@/stores/auth'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import BaseButton from '@/components/BaseButton.vue'
import BaseInput from '@/components/BaseInput.vue'
import { HEALTH_GROUP_LABELS, type HealthGroup } from '@/types/api'

const bmstu = useBmstuStore()
const auth = useAuthStore()
const { success, error: toastError, info } = useToast()

const healthGroupSchema = z.enum(['BASIC', 'PREPARATORY', 'SPECIAL_MEDICAL', 'AFK'], {
  errorMap: () => ({ message: 'Выбери группу здоровья' }),
})

const schema = toTypedSchema(
  z.object({
    login: z.string().min(3, 'Минимум 3 символа'),
    password: z.string().min(4, 'Минимум 4 символа'),
    health_group: healthGroupSchema,
  }),
)

const { defineField, handleSubmit, errors, resetForm, setFieldValue } = useForm({
  validationSchema: schema,
  initialValues: { health_group: 'BASIC' as HealthGroup },
})
const [login, loginAttrs] = defineField('login')
const [password, passwordAttrs] = defineField('password')
const [healthGroup] = defineField('health_group')
const submitting = ref(false)
const tgLinking = ref(false)
const refreshing = ref(false)

/**
 * Локальный toggle между read-only view и edit form в секции BMSTU LKS.
 *
 * Mental model:
 *  - `flags.linked === false` — креды никогда не сохранялись, форма видна сразу.
 *  - `flags.linked === true && !isEditing` — read-only summary (status + health
 *    group + last login + кнопки «Изменить» / «Удалить»).
 *  - `flags.linked === true && isEditing` — форма поверх read-only с кнопкой
 *    «Отмена», pre-filled health_group из status (login/password всегда пустые,
 *    бэк не отдаёт их обратно by design).
 *
 * Сбрасывается в `false` после успешного `setCreds` и при `deleteCreds`,
 * чтобы UI вернулся в канонический режим.
 */
const isEditing = ref(false)

/**
 * Опции для radio-cards группы здоровья. Каждая — `{ value, label, icon, hint }`.
 * Метки берутся из единого `HEALTH_GROUP_LABELS`, иконки — для визуальной
 * дифференциации (стиль остальных секций Settings: иконка + emerald-акцент).
 */
const healthGroupOptions: ReadonlyArray<{
  value: HealthGroup
  label: string
  icon: typeof Activity
  hint: string
}> = [
  { value: 'BASIC', label: HEALTH_GROUP_LABELS.BASIC, icon: Activity, hint: 'без ограничений' },
  {
    value: 'PREPARATORY',
    label: HEALTH_GROUP_LABELS.PREPARATORY,
    icon: Heart,
    hint: 'лёгкие ограничения',
  },
  {
    value: 'SPECIAL_MEDICAL',
    label: HEALTH_GROUP_LABELS.SPECIAL_MEDICAL,
    icon: Stethoscope,
    hint: 'СМГ',
  },
  { value: 'AFK', label: HEALTH_GROUP_LABELS.AFK, icon: Accessibility, hint: 'адаптивная' },
] as const

onMounted(async () => {
  try {
    await Promise.all([bmstu.fetchStatus(), auth.fetchMe()])
    // Если креды уже привязаны, подставим текущую группу здоровья в форму
    // (UX: юзер видит, что выбрано, и может изменить с минимумом кликов).
    if (bmstu.status.health_group) {
      setFieldValue('health_group', bmstu.status.health_group)
    }
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
})

const onSubmitCreds = handleSubmit(async (values) => {
  submitting.value = true
  try {
    await bmstu.setCreds({
      login: values.login,
      password: values.password,
      health_group: values.health_group,
    })
    success('Креды сохранены, test-логин прошёл')
    // После успеха обнуляем login/password (бэк их не возвращает, плюс пустые
    // поля в read-only-state менее путают, чем сохранённые); health_group
    // оставляем как есть — read-only view показывает её отдельной строкой.
    resetForm({ values: { health_group: values.health_group } })
    // Возвращаемся в read-only режим — юзер видит badge + summary.
    isEditing.value = false
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    submitting.value = false
  }
})

async function onDeleteCreds(): Promise<void> {
  if (!window.confirm('Удалить креды BMSTU? Алёрты перестанут приходить.')) {
    return
  }
  try {
    await bmstu.deleteCreds()
    info('Креды удалены')
    // После delete форма должна появиться сразу (status снова NOT_LINKED) —
    // сбросим edit-flag, иначе showForm будет рассчитан по stale значению.
    isEditing.value = false
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
}

/**
 * Включает edit-mode для секции BMSTU.
 *
 * Pre-fill: подставляет health_group из текущего status в форму, чтобы юзер
 * не выбирал её заново. Login/password всегда остаются пустыми — бэк хранит
 * пароль в зашифрованном виде и физически не отдаёт его, поэтому показать
 * prior значение мы не можем.
 */
function startEditing(): void {
  if (bmstu.status.health_group) {
    setFieldValue('health_group', bmstu.status.health_group)
  }
  isEditing.value = true
}

/**
 * Отменяет edit-mode и возвращает секцию в read-only view.
 *
 * Side-effect: ресетит инпуты login/password (чтобы при следующем «Изменить»
 * не оставались введённые ранее значения).
 */
function cancelEditing(): void {
  resetForm({ values: { health_group: bmstu.status.health_group ?? 'BASIC' } })
  isEditing.value = false
}

async function onLinkTelegram(): Promise<void> {
  tgLinking.value = true
  try {
    const init = await auth.linkTelegram()
    window.open(init.deeplink, '_blank', 'noopener,noreferrer')
    info(
      'Открыли Telegram. Нажми Start у бота — после этого статус обновится. ' +
        `Код: ${init.code}`,
    )
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    tgLinking.value = false
  }
}

async function refreshTgStatus(): Promise<void> {
  refreshing.value = true
  try {
    await auth.fetchMe()
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    refreshing.value = false
  }
}

/**
 * Подпись и стиль badge-а статуса BMSTU.
 *
 * Возвращает класс-name из дизайн-системы (`badge-emerald` / `badge-rose` ...).
 */
const statusBadge = computed(() => {
  switch (bmstu.status.status) {
    case 'VALID':
      return { cls: 'badge-emerald', label: 'привязан, активен', icon: CheckCircle2 }
    case 'INVALID':
      return { cls: 'badge-rose', label: 'невалиден — обнови пароль', icon: AlertTriangle }
    case 'EXPIRED':
      return { cls: 'badge-amber', label: 'сессия истекла', icon: Clock }
    default:
      return { cls: 'badge-zinc', label: 'не привязан', icon: AlertTriangle }
  }
})

/**
 * Badge с человекочитаемой подписью текущей группы здоровья.
 *
 * `null` означает, что креды не привязаны или сервер не вернул `health_group`
 * (бэквард-кейс) — компонент не рендерится.
 */
const currentHealthGroupLabel = computed<string | null>(() => {
  const hg = bmstu.status.health_group
  return hg ? HEALTH_GROUP_LABELS[hg] : null
})

/**
 * Видна ли форма ввода кредов.
 *
 * Логика:
 *  - креды не сохранены — форма всегда видна (юзер должен ввести впервые);
 *  - креды сохранены, но юзер кликнул «Изменить» — форма поверх read-only.
 *
 * Если `false` — рендерится read-only summary (badge + health_group + last
 * login + кнопки «Изменить» / «Удалить»).
 */
const showForm = computed<boolean>(() => !bmstu.flags.linked || isEditing.value)

/**
 * Edge case: креды сохранены, но последний логин был неуспешен (INVALID или
 * EXPIRED). В этом случае мы остаёмся в read-only режиме, но подсвечиваем
 * warning + CTA «Изменить» как primary (юзеру нужно пересохранить пароль или
 * обновить сессию).
 */
const showInvalidWarning = computed<boolean>(
  () => bmstu.flags.linked && !bmstu.flags.valid,
)
</script>

<template>
  <section class="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-10">
    <header class="mb-8">
      <h1 class="h-section">
        Настройки
      </h1>
      <p class="mt-1 text-sm text-zinc-500">
        Подключи Telegram и BMSTU LKS — алёрты заработают сразу после.
      </p>
    </header>

    <div class="space-y-5">
      <!-- ===== Аккаунт ===== -->
      <article class="card">
        <header class="mb-4 flex items-center gap-3">
          <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-zinc-800/80 text-zinc-300">
            <UserIcon class="h-4 w-4" />
          </span>
          <div class="flex-1">
            <h2 class="text-base font-semibold text-zinc-50">
              Аккаунт
            </h2>
            <p class="text-xs text-zinc-500">
              Email и активная сессия
            </p>
          </div>
        </header>

        <div class="rounded-xl border border-zinc-800/60 bg-zinc-950/40 p-3 font-mono text-sm text-zinc-300">
          {{ auth.user?.email ?? '—' }}
        </div>

        <BaseButton
          variant="secondary"
          size="sm"
          class="mt-4"
          @click="auth.logout"
        >
          <template #icon-left>
            <LogOut class="h-3.5 w-3.5" />
          </template>
          Выйти из аккаунта
        </BaseButton>
      </article>

      <!-- ===== Telegram ===== -->
      <article class="card">
        <header class="mb-4 flex items-start justify-between gap-3">
          <div class="flex items-center gap-3">
            <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-sky-500/15 text-sky-300">
              <Send class="h-4 w-4" />
            </span>
            <div>
              <h2 class="text-base font-semibold text-zinc-50">
                Telegram
              </h2>
              <p class="text-xs text-zinc-500">
                Канал доставки алёртов
              </p>
            </div>
          </div>
          <span
            v-if="auth.user?.telegram_chat_id"
            class="badge-emerald"
          >
            <CheckCircle2 class="h-3 w-3" />
            привязан
          </span>
          <span
            v-else
            class="badge-zinc"
          >
            не привязан
          </span>
        </header>

        <p
          v-if="auth.user?.telegram_chat_id"
          class="mb-4 rounded-xl border border-zinc-800/60 bg-zinc-950/40 p-3 font-mono text-xs text-zinc-300"
        >
          chat_id · <span class="text-zinc-100">{{ auth.user.telegram_chat_id }}</span>
        </p>
        <p
          v-else
          class="mb-4 text-sm text-zinc-400"
        >
          Без привязки телеграма алёрты будут приходить только в браузер по SSE.
          Нажми кнопку ниже, открой бота, отправь команду — статус обновится здесь.
        </p>

        <div class="flex flex-wrap gap-2">
          <BaseButton
            :loading="tgLinking"
            @click="onLinkTelegram"
          >
            <template #icon-left>
              <ExternalLink class="h-3.5 w-3.5" />
            </template>
            Привязать Telegram
          </BaseButton>
          <BaseButton
            variant="secondary"
            :loading="refreshing"
            @click="refreshTgStatus"
          >
            <template #icon-left>
              <RefreshCw class="h-3.5 w-3.5" />
            </template>
            Обновить статус
          </BaseButton>
        </div>
      </article>

      <!-- ===== BMSTU LKS ===== -->
      <article class="card">
        <header class="mb-4 flex items-start justify-between gap-3">
          <div class="flex items-center gap-3">
            <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-brand-500/15 text-brand-300">
              <GraduationCap class="h-4 w-4" />
            </span>
            <div>
              <h2 class="text-base font-semibold text-zinc-50">
                BMSTU LKS
              </h2>
              <p class="text-xs text-zinc-500">
                Креды для опроса расписания
              </p>
            </div>
          </div>
          <span :class="statusBadge.cls">
            <component
              :is="statusBadge.icon"
              class="h-3 w-3"
            />
            {{ statusBadge.label }}
          </span>
        </header>

        <!-- Warning, когда креды сохранены, но последний логин неуспешен.
              CTA «Изменить» внутри read-only-блока ниже становится primary. -->
        <p
          v-if="showInvalidWarning"
          class="mb-3 flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/[0.08] p-3 text-xs text-amber-200"
        >
          <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            Последний логин не прошёл — возможно, ты сменил пароль в LKS или
            сессия истекла. Нажми «Изменить креды», чтобы пересохранить.
          </span>
        </p>
        <p
          v-if="bmstu.status.last_error"
          class="mb-3 rounded-xl border border-rose-500/30 bg-rose-500/[0.06] p-3 text-xs text-rose-300"
        >
          Ошибка: {{ bmstu.status.last_error }}
        </p>

        <Transition
          mode="out-in"
          enter-active-class="transition-all duration-200 ease-out"
          enter-from-class="-translate-y-1 opacity-0"
          enter-to-class="translate-y-0 opacity-100"
          leave-active-class="transition-all duration-150 ease-in"
          leave-from-class="translate-y-0 opacity-100"
          leave-to-class="-translate-y-1 opacity-0"
        >
          <!-- ============ READ-ONLY summary ============ -->
          <div
            v-if="!showForm"
            key="readonly"
            class="space-y-3"
          >
            <div class="rounded-xl border border-zinc-800/60 bg-zinc-950/40 p-4">
              <dl class="space-y-2 text-sm">
                <div
                  v-if="currentHealthGroupLabel"
                  class="flex items-center gap-2"
                >
                  <dt class="flex items-center gap-1.5 text-xs text-zinc-500">
                    <Activity class="h-3.5 w-3.5" />
                    Группа здоровья
                  </dt>
                  <dd class="ml-auto">
                    <span class="badge-emerald">{{ currentHealthGroupLabel }}</span>
                  </dd>
                </div>
                <div
                  v-if="bmstu.status.last_login_at"
                  class="flex items-center gap-2"
                >
                  <dt class="flex items-center gap-1.5 text-xs text-zinc-500">
                    <Clock class="h-3.5 w-3.5" />
                    Последний логин
                  </dt>
                  <dd class="ml-auto font-mono text-xs text-zinc-300">
                    {{ new Date(bmstu.status.last_login_at).toLocaleString('ru-RU') }}
                  </dd>
                </div>
              </dl>
            </div>

            <div class="flex flex-wrap gap-2">
              <BaseButton
                type="button"
                :variant="showInvalidWarning ? 'primary' : 'secondary'"
                @click="startEditing"
              >
                <template #icon-left>
                  <Pencil class="h-3.5 w-3.5" />
                </template>
                Изменить креды
              </BaseButton>
              <BaseButton
                type="button"
                variant="danger"
                @click="onDeleteCreds"
              >
                <template #icon-left>
                  <Trash2 class="h-3.5 w-3.5" />
                </template>
                Удалить
              </BaseButton>
            </div>
          </div>

          <!-- ============ EDIT form ============ -->
          <form
            v-else
            key="form"
            class="space-y-4"
            novalidate
            @submit="onSubmitCreds"
          >
            <!-- Hint для edit-режима: пароль не возвращается с бэка. -->
            <p
              v-if="bmstu.flags.linked"
              class="rounded-xl border border-zinc-800/60 bg-zinc-950/40 p-3 text-xs text-zinc-400"
            >
              Введи логин и пароль заново — мы не храним их в открытом виде,
              поэтому prior значения подставить не можем.
            </p>

            <BaseInput
              v-model="login"
              v-bind="loginAttrs"
              label="Логин LKS"
              placeholder="ivan_ivanov"
              autocomplete="off"
              required
              :error="errors.login"
            >
              <template #icon-left>
                <UserIcon class="h-4 w-4" />
              </template>
            </BaseInput>
            <BaseInput
              v-model="password"
              v-bind="passwordAttrs"
              label="Пароль LKS"
              type="password"
              placeholder="••••••••"
              autocomplete="new-password"
              required
              :error="errors.password"
            >
              <template #icon-left>
                <Lock class="h-4 w-4" />
              </template>
            </BaseInput>

            <!-- Radio-cards: группа здоровья. Стиль = icon-tile (как остальные
                  секции Settings), выбранный вариант подсвечивается emerald-кольцом. -->
            <fieldset>
              <legend class="form-label">
                Группа здоровья <span
                  class="text-rose-400"
                  aria-hidden="true"
                >*</span>
              </legend>
              <div
                class="grid grid-cols-1 gap-2 sm:grid-cols-2"
                role="radiogroup"
                aria-label="Группа здоровья"
              >
                <label
                  v-for="opt in healthGroupOptions"
                  :key="opt.value"
                  :class="[
                    'group flex cursor-pointer items-start gap-3 rounded-xl border p-3 transition-all',
                    healthGroup === opt.value
                      ? 'border-emerald-500/60 bg-emerald-500/[0.08] shadow-[0_0_0_1px_rgba(16,185,129,0.18)]'
                      : 'border-zinc-800/80 bg-zinc-950/40 hover:border-zinc-700 hover:bg-zinc-900/60',
                  ]"
                >
                  <input
                    v-model="healthGroup"
                    type="radio"
                    name="health_group"
                    :value="opt.value"
                    class="sr-only"
                    :aria-checked="healthGroup === opt.value"
                  >
                  <span
                    :class="[
                      'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl transition-colors',
                      healthGroup === opt.value
                        ? 'bg-emerald-500/15 text-emerald-300'
                        : 'bg-zinc-800/80 text-zinc-300 group-hover:text-zinc-100',
                    ]"
                  >
                    <component
                      :is="opt.icon"
                      class="h-4 w-4"
                    />
                  </span>
                  <span class="flex min-w-0 flex-1 flex-col">
                    <span class="text-sm font-semibold text-zinc-50">
                      {{ opt.label }}
                    </span>
                    <span class="text-xs text-zinc-500">
                      {{ opt.hint }}
                    </span>
                  </span>
                  <CheckCircle2
                    v-if="healthGroup === opt.value"
                    class="h-4 w-4 shrink-0 text-emerald-300"
                    aria-hidden="true"
                  />
                </label>
              </div>
              <p
                v-if="errors.health_group"
                class="form-error"
              >
                {{ errors.health_group }}
              </p>
            </fieldset>

            <div class="flex flex-wrap gap-2">
              <BaseButton
                type="submit"
                :loading="submitting"
              >
                Сохранить
              </BaseButton>
              <BaseButton
                v-if="bmstu.flags.linked"
                type="button"
                variant="secondary"
                :disabled="submitting"
                @click="cancelEditing"
              >
                <template #icon-left>
                  <XIcon class="h-3.5 w-3.5" />
                </template>
                Отмена
              </BaseButton>
            </div>
          </form>
        </Transition>
      </article>

      <!-- ===== Безопасность ===== -->
      <article class="card border-emerald-500/20">
        <header class="mb-3 flex items-center gap-3">
          <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-emerald-500/15 text-emerald-300">
            <ShieldCheck class="h-4 w-4" />
          </span>
          <div>
            <h2 class="text-base font-semibold text-zinc-50">
              Безопасность
            </h2>
            <p class="text-xs text-zinc-500">
              Как мы храним креды
            </p>
          </div>
        </header>
        <p class="text-sm text-zinc-400">
          Пароль от LKS шифруется на сервере
          <code class="rounded bg-zinc-900 px-1.5 py-0.5 font-mono text-xs text-emerald-300">
            AES-256-GCM
          </code>
          c per-user nonce — мы физически не видим его в открытом виде.
          Refresh-токен живёт в
          <code class="rounded bg-zinc-900 px-1.5 py-0.5 font-mono text-xs text-emerald-300">
            httpOnly cookie
          </code>
          , недоступном из JS.
        </p>
      </article>
    </div>
  </section>
</template>
