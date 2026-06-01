<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import BaseButton from '@/components/BaseButton.vue'
import BaseInput from '@/components/BaseInput.vue'

const auth = useAuthStore()
const router = useRouter()
const { success, error: toastError } = useToast()

const schema = toTypedSchema(
  z
    .object({
      email: z.string().email('Неверный формат email'),
      password: z.string().min(8, 'Минимум 8 символов'),
      confirm: z.string().min(8, 'Минимум 8 символов'),
    })
    .refine((v) => v.password === v.confirm, {
      message: 'Пароли не совпадают',
      path: ['confirm'],
    }),
)

const { defineField, handleSubmit, errors } = useForm({ validationSchema: schema })
const [email, emailAttrs] = defineField('email')
const [password, passwordAttrs] = defineField('password')
const [confirm, confirmAttrs] = defineField('confirm')
const submitting = ref(false)

const onSubmit = handleSubmit(async (values) => {
  submitting.value = true
  try {
    await auth.register(values.email, values.password)
    // Register не возвращает токены — сразу логинимся.
    await auth.login(values.email, values.password)
    success('Аккаунт создан')
    await router.push('/')
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    submitting.value = false
  }
})
</script>

<template>
  <main class="flex min-h-full items-center justify-center px-4 py-12">
    <div class="w-full max-w-sm">
      <h1 class="mb-6 text-center text-2xl font-bold text-gray-900">
        Регистрация
      </h1>
      <form
        class="space-y-4"
        novalidate
        @submit="onSubmit"
      >
        <BaseInput
          v-model="email"
          v-bind="emailAttrs"
          label="Email"
          type="email"
          autocomplete="email"
          required
          :error="errors.email"
        />
        <BaseInput
          v-model="password"
          v-bind="passwordAttrs"
          label="Пароль"
          type="password"
          autocomplete="new-password"
          required
          :error="errors.password"
          hint="Минимум 8 символов"
        />
        <BaseInput
          v-model="confirm"
          v-bind="confirmAttrs"
          label="Подтверждение пароля"
          type="password"
          autocomplete="new-password"
          required
          :error="errors.confirm"
        />
        <BaseButton
          type="submit"
          :loading="submitting"
          class="w-full"
        >
          Создать аккаунт
        </BaseButton>
      </form>
      <p class="mt-4 text-center text-sm text-gray-600">
        Уже есть аккаунт?
        <RouterLink
          to="/login"
          class="font-medium text-brand-600 hover:text-brand-700"
        >
          Войти
        </RouterLink>
      </p>
    </div>
  </main>
</template>
