# ADR 0003 — Pure HTTP Keycloak OIDC vs Chromedp/Selenium

**Дата:** 2026-06-02
**Статус:** Accepted

## Контекст

Текущая Python-версия использует Selenium + Chromium для логина в BMSTU LKS:
заходит на `/profile`, Keycloak редиректит на форму, Selenium заполняет
поля и сабмитит. Стоимость:
- ~200 MB RAM на каждый логин (Chromium процесс).
- 30–50 секунд на сессию.
- Зависимость от webdriver-manager, обновлений Chromium.
- Headful/headless нестабильность.

При 1000 юзерах: 200 GB RAM просто на логины — нереалистично.

Варианты:
1. **Pure HTTP Keycloak OIDC** — Go-клиент с `http.Client`+cookiejar, парсим
   HTML формы Keycloak с `golang.org/x/net/html`, постим креды, следуем
   30x редиректам.
2. **Chromedp** — embedded Chromium через CDP, легче Selenium, но всё ещё
   браузер.
3. **Keycloak ROPC grant** (Resource Owner Password Credentials) — если
   realm BMSTU его поддерживает, можно сразу `POST /token` и получить
   access_token без HTML формы.

## Решение

Идём по **варианту 1 (pure HTTP)** как основной, с **флагом fallback на
chromedp** для случая, если Keycloak когда-нибудь подкрутит JS-челлендж
(reCAPTCHA, anti-bot heuristic) и форма перестанет работать без браузера.

Параллельно — отдельная research-задача проверить, поддерживает ли realm
BMSTU **ROPC grant** (вариант 3). Если да — это ещё проще: один POST на
`/token` без HTML-парсинга.

## Обоснование

| Критерий | Pure HTTP | Chromedp | ROPC |
|---|---|---|---|
| RAM на логин | ~0 MB | 100–200 MB | ~0 MB |
| Время логина | 150–300 ms | 5–15 s | 50–150 ms |
| Зависимости | stdlib + x/net/html | chromedp + Chromium ~150 MB | stdlib |
| Хрупкость к редизайну Keycloak | HTML formaction может измениться | Низкая (рендерит как браузер) | Очень низкая (REST API) |
| Хрупкость к JS-челленджу | Высокая (нет JS-runtime) | Низкая | Низкая |
| Возможно ли использовать ROPC у BMSTU | N/A | N/A | Не проверено, риск |

Pure HTTP даёт 150x ускорение и нулевой RAM-overhead. Цена — парсинг HTML
формы Keycloak (action, hidden inputs, form fields), который может
сломаться при апгрейде Keycloak. Митигация:
- Изолировано в `bmstu-svc/internal/oidc/` с тестами на захваченных
  фикстурах HTML.
- Алерт в Prometheus на рост `bmstu_oidc_failure_rate`.
- Env-flag `OIDC_USE_BROWSER=1` для аварийного fallback на chromedp
  без передеплоя.
- Research-задача параллельно: попробовать ROPC — если зайдёт, можем вообще
  выкинуть HTML-парс.

## Цена

- Команде нужно понимать, как работает Keycloak login flow (state, nonce,
  form_action, set-cookie chain) — это разовая инвестиция, документируется
  в `bmstu-svc/README.md`.
- Тесты должны включать happy path (фикстура HTML Keycloak BMSTU + проверка
  cookies) и краевые: 401 на форме, redirect loop, истёкший state.

## Альтернативы

**Chromedp без HTTP fallback** — отвергнут, не решает RAM-проблему.

**Selenium как есть, портированный** — нет, ровно та же проблема, ещё и
flaky.

**Только ROPC без HTTP fallback** — рискованно, не знаем, включен ли он
в realm BMSTU; некоторые realms имеют `direct_access_grants_enabled: false`.

## Последствия

- bmstu-svc реализует OIDC client сам в `internal/oidc/` (~300 строк Go).
- Cookies сохраняются в `bmstu_db.bmstu_sessions` как gob+AES-GCM blob
  с TTL равной expires из set-cookie. При запуске сервиса cookies
  восстанавливаются в `http.CookieJar` в RAM.
- При получении 401 от `/groups`: один retry с RefreshSession, при втором
  401 — статус becomes INVALID, юзер получает SendDirect через notifier.
- Параллельно researcher-агент проверяет ROPC. Если есть — переключим
  на ROPC за полдня (отдельный PR), HTML-парс останется как fallback.

## Связанные

- [0001 — Microservices vs monolith](./0001-microservices-vs-monolith.md)
