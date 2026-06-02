<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Mail, Lock, Zap, Sparkles, CheckCircle2 } from 'lucide-vue-next'
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

/** Маркетинговые буллеты на hero-стороне. */
const perks = [
  'Алёрты в Telegram за секунды',
  'AES-256 шифрование пароля LKS',
  'До 18 фильтров на пользователя',
]
</script>

<template>
  <main class="flex min-h-full">
    <!-- ===== Brand hero (desktop only ≥ lg) ===== -->
    <aside
      class="relative hidden flex-1 flex-col justify-between overflow-hidden border-r border-zinc-800/80 bg-zinc-950 p-10 lg:flex"
    >
      <div
        class="pointer-events-none absolute inset-0 bg-gradient-hero opacity-90"
        aria-hidden="true"
      />
      <div
        class="pointer-events-none absolute inset-0 bg-grid-zinc opacity-30"
        aria-hidden="true"
      />

      <RouterLink
        to="/"
        class="relative z-10 flex items-center gap-3"
      >
        <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-brand shadow-glow">
          <Zap class="h-6 w-6 text-white" />
        </span>
        <span class="text-lg font-bold tracking-tight text-zinc-50">
          fizcultor
        </span>
      </RouterLink>

      <div class="relative z-10 max-w-md">
        <span class="badge-violet mb-6">
          <Sparkles class="h-3 w-3" />
          5 секунд до первого фильтра
        </span>
        <h1 class="h-display mb-4">
          Присоединяйся
          <span class="bg-gradient-to-br from-brand-300 via-brand-400 to-emerald-200 bg-clip-text text-transparent">
            к команде
          </span>
        </h1>
        <p class="mb-8 text-lg leading-relaxed text-zinc-400">
          Регистрация занимает 10 секунд. Привязка к LKS — ещё минуту.
          Дальше — алёрты сами найдут свободные слоты.
        </p>

        <ul class="space-y-3">
          <li
            v-for="(perk, i) in perks"
            :key="i"
            class="flex items-start gap-3 text-sm text-zinc-300"
          >
            <CheckCircle2 class="mt-0.5 h-4 w-4 shrink-0 text-brand-400" />
            <span>{{ perk }}</span>
          </li>
        </ul>
      </div>

      <p class="relative z-10 font-mono text-2xs text-zinc-600">
        v0.1 · Wave-3 release
      </p>
    </aside>

    <!-- ===== Register form ===== -->
    <section class="flex flex-1 items-center justify-center px-4 py-12 sm:px-8">
      <div class="w-full max-w-sm">
        <div class="mb-8 flex flex-col items-center text-center lg:hidden">
          <span class="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-gradient-brand shadow-glow">
            <Zap class="h-6 w-6 text-white" />
          </span>
          <h2 class="text-2xl font-bold tracking-tight text-zinc-50">
            fizcultor
          </h2>
          <p class="mt-1 text-xs text-zinc-500">
            Создай аккаунт за 10 секунд
          </p>
        </div>

        <div class="mb-8 hidden lg:block">
          <h2 class="text-2xl font-bold tracking-tight text-zinc-50">
            Создать аккаунт
          </h2>
          <p class="mt-1.5 text-sm text-zinc-400">
            Регистрация бесплатна. Без спама.
          </p>
        </div>

        <form
          class="space-y-5"
          novalidate
          @submit="onSubmit"
        >
          <BaseInput
            v-model="email"
            v-bind="emailAttrs"
            label="Email"
            type="email"
            placeholder="you@bmstu.ru"
            autocomplete="email"
            required
            :error="errors.email"
          >
            <template #icon-left>
              <Mail class="h-4 w-4" />
            </template>
          </BaseInput>
          <BaseInput
            v-model="password"
            v-bind="passwordAttrs"
            label="Пароль"
            type="password"
            placeholder="минимум 8 символов"
            autocomplete="new-password"
            required
            :error="errors.password"
            hint="Минимум 8 символов"
          >
            <template #icon-left>
              <Lock class="h-4 w-4" />
            </template>
          </BaseInput>
          <BaseInput
            v-model="confirm"
            v-bind="confirmAttrs"
            label="Подтверждение пароля"
            type="password"
            placeholder="ещё раз"
            autocomplete="new-password"
            required
            :error="errors.confirm"
          >
            <template #icon-left>
              <Lock class="h-4 w-4" />
            </template>
          </BaseInput>
          <BaseButton
            type="submit"
            :loading="submitting"
            block
            size="lg"
          >
            Создать аккаунт
          </BaseButton>
        </form>

        <p class="mt-6 text-center text-sm text-zinc-500">
          Уже есть аккаунт?
          <RouterLink
            to="/login"
            class="font-semibold text-brand-400 transition-colors hover:text-brand-300"
          >
            Войти →
          </RouterLink>
        </p>
      </div>
    </section>
  </main>
</template>
