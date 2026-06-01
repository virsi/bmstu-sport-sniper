<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useBmstuStore } from '@/stores/bmstu'
import { useAuthStore } from '@/stores/auth'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import BaseButton from '@/components/BaseButton.vue'
import BaseInput from '@/components/BaseInput.vue'

const bmstu = useBmstuStore()
const auth = useAuthStore()
const { success, error: toastError, info } = useToast()

const schema = toTypedSchema(
  z.object({
    login: z.string().min(3, 'Минимум 3 символа'),
    password: z.string().min(4, 'Минимум 4 символа'),
  }),
)

const { defineField, handleSubmit, errors, resetForm } = useForm({ validationSchema: schema })
const [login, loginAttrs] = defineField('login')
const [password, passwordAttrs] = defineField('password')
const submitting = ref(false)
const tgLinking = ref(false)
const refreshing = ref(false)

onMounted(async () => {
  try {
    await Promise.all([bmstu.fetchStatus(), auth.fetchMe()])
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
})

const onSubmitCreds = handleSubmit(async (values) => {
  submitting.value = true
  try {
    await bmstu.setCreds(values)
    success('Креды сохранены, test-логин прошёл')
    resetForm()
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
  } catch (err) {
    toastError(extractErrorMessage(err))
  }
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

/** Подпись badge-а статуса BMSTU. */
const statusBadge = computed(() => {
  switch (bmstu.status.status) {
    case 'VALID':
      return { cls: 'bg-green-100 text-green-800', label: 'привязан, активен' }
    case 'INVALID':
      return { cls: 'bg-red-100 text-red-800', label: 'невалиден — обнови пароль' }
    case 'EXPIRED':
      return { cls: 'bg-amber-100 text-amber-800', label: 'сессия истекла' }
    default:
      return { cls: 'bg-gray-100 text-gray-700', label: 'не привязан' }
  }
})
</script>

<template>
  <section class="mx-auto max-w-2xl px-4 py-6">
    <h1 class="mb-6 text-2xl font-bold text-gray-900">
      Настройки
    </h1>

    <article class="card mb-6">
      <header class="mb-2 flex items-center justify-between">
        <h2 class="text-lg font-semibold">
          Telegram
        </h2>
        <span
          v-if="auth.user?.telegram_chat_id"
          class="rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800"
        >
          привязан
        </span>
        <span
          v-else
          class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700"
        >
          не привязан
        </span>
      </header>
      <p
        v-if="auth.user?.telegram_chat_id"
        class="mb-3 text-sm text-gray-700"
      >
        chat ID: <code>{{ auth.user.telegram_chat_id }}</code>
      </p>
      <p
        v-else
        class="mb-3 text-sm text-gray-600"
      >
        Без привязки телеграма алёрты будут приходить только в браузер по SSE.
        Нажми «Привязать», открой бота, отправь команду — статус обновится здесь.
      </p>
      <div class="flex gap-2">
        <BaseButton
          variant="secondary"
          :loading="tgLinking"
          @click="onLinkTelegram"
        >
          Привязать Telegram
        </BaseButton>
        <BaseButton
          variant="secondary"
          :loading="refreshing"
          @click="refreshTgStatus"
        >
          Обновить статус
        </BaseButton>
      </div>
    </article>

    <article class="card mb-6">
      <header class="mb-2 flex items-center justify-between">
        <h2 class="text-lg font-semibold">
          BMSTU LKS
        </h2>
        <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', statusBadge.cls]">
          {{ statusBadge.label }}
        </span>
      </header>
      <div class="mb-3 rounded-md bg-amber-50 p-3 text-xs text-amber-900">
        <strong>Внимание.</strong> Пароль от LKS шифруется на сервере AES-256-GCM,
        мы не видим его в открытом виде. Тем не менее, не используй здесь основной
        пароль БМГТУ — если есть возможность, создай отдельный.
      </div>
      <p
        v-if="bmstu.status.last_login_at"
        class="mb-3 text-sm text-gray-600"
      >
        Последний успешный логин:
        {{ new Date(bmstu.status.last_login_at).toLocaleString('ru-RU') }}
      </p>
      <p
        v-if="bmstu.status.last_error"
        class="mb-3 text-sm text-red-700"
      >
        Ошибка: {{ bmstu.status.last_error }}
      </p>
      <form
        class="space-y-4"
        novalidate
        @submit="onSubmitCreds"
      >
        <BaseInput
          v-model="login"
          v-bind="loginAttrs"
          label="Логин LKS"
          autocomplete="off"
          required
          :error="errors.login"
        />
        <BaseInput
          v-model="password"
          v-bind="passwordAttrs"
          label="Пароль LKS"
          type="password"
          autocomplete="new-password"
          required
          :error="errors.password"
        />
        <div class="flex gap-2">
          <BaseButton
            type="submit"
            :loading="submitting"
          >
            Сохранить
          </BaseButton>
          <BaseButton
            v-if="bmstu.flags.linked"
            type="button"
            variant="danger"
            :disabled="submitting"
            @click="onDeleteCreds"
          >
            Удалить
          </BaseButton>
        </div>
      </form>
    </article>

    <article class="card">
      <h2 class="mb-2 text-lg font-semibold">
        Аккаунт
      </h2>
      <p class="text-sm text-gray-700">
        Email: <code>{{ auth.user?.email ?? '—' }}</code>
      </p>
      <BaseButton
        variant="secondary"
        class="mt-3"
        @click="auth.logout"
      >
        Выйти
      </BaseButton>
    </article>
  </section>
</template>
