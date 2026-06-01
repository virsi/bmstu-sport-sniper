# Open Questions (для backend Wave 2/3)

## От frontend-dev (Wave 1)

1. **JWT в SSE.** Сейчас фронт шлёт `?access=<token>` в query на `/api/stream`. Альтернатива — one-time ticket через `POST /api/stream/ticket`. Решить в gateway-svc design. Рекомендация: ticket (короткоживущий 30 с в Redis/in-memory), не светить JWT в access-log.

2. **Refresh storage.** В V1 оба токена в `localStorage` (помечено TODO security review). V2 — переехать refresh на `httpOnly; Secure; SameSite=Strict` cookie. Влияет на `/auth/refresh`: без тела, читает cookie. Учесть в auth-svc gRPC и gateway proxy.

3. **`Slot.location` и `Slot.teacherRating`.** Опциональные? Подтвердить в `backend/proto/common/v1/common.proto`. По плану — да (`teacher` через teachers-svc lookup).

4. **CSV-ввод `groupKeywords`/`teachers` в фильтрах.** Сейчас фронт шлёт `string[]` (через запятую парсит). Бек должен принимать массив, не строку. Проверить в Filter proto.

## От researcher (Wave 0)

5. **`p4sess` TTL.** Точно неизвестно (нет кредов). Поллер: на 401 → один полный re-login + exponential backoff. Не хелперить refresh руками.

6. **Brute-force protection Keycloak.** При множественных failed logins → temporary disable аккаунта. bmstu-svc: на `invalid_grant`-style errors не ретраить, помечать creds как невалидные, нотифай юзера.

7. **CAPTCHA.** Сейчас НЕТ. На случай появления — `OIDC_USE_BROWSER=1` env-флаг + `fallback.go.disabled` плейсхолдер chromedp.

## TODO для wave 2

- [ ] Все `string[]` в Filter proto → `repeated string`, не `string`
- [ ] `bmstu-svc` cookies persistence: AES-GCM + gob([]*http.Cookie)
- [ ] gateway: реализовать stream-ticket endpoint
