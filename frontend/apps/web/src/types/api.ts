/**
 * DTO-типы REST API gateway-svc.
 *
 * Источник правды — `docs/api.md` + protobuf-контракты в `backend/proto/`.
 * Здесь — узкие зеркала только тех полей, что использует фронт.
 *
 * Соглашения:
 * - Имена полей — snake_case, как REST/proto. Это сознательный выбор: не платим
 *   за маппинг туда-сюда, типы 1:1 с тем, что приходит в сети.
 * - Все ID — `string`. `User.id` это BIGSERIAL, который auth-svc stringify-ит.
 * - Все таймстампы — ISO-8601 в строках.
 * - Поля, помеченные в proto `optional`, здесь `?:` (могут отсутствовать или быть null).
 * - Поля, которых нет в proto, но возвращает REST (`teacher_rating`), — отмечены отдельно.
 */

/** День недели в формате backend (proto enum DayOfWeek, без префикса). */
export type DayOfWeek =
  | 'ANY'
  | 'MONDAY'
  | 'TUESDAY'
  | 'WEDNESDAY'
  | 'THURSDAY'
  | 'FRIDAY'
  | 'SATURDAY'
  | 'SUNDAY'

/** Состояние линковки BMSTU-кредов в формате backend. */
export type BmstuLinkStatus = 'NOT_LINKED' | 'VALID' | 'INVALID' | 'EXPIRED'

/**
 * Группа здоровья студента БМГТУ для занятий физкультурой.
 *
 * Значения совпадают с `common.v1.HealthGroup` (без префикса `HEALTH_GROUP_*`)
 * и со CHECK в БД (`backend/migrations/bmstu_db/00003_health_group.sql`).
 * На стороне bmstu-svc определяет, какой `SEMESTER_UUID_*` идёт в LKS API
 * при FetchGroups.
 */
export type HealthGroup = 'BASIC' | 'PREPARATORY' | 'SPECIAL_MEDICAL' | 'AFK'

/**
 * Человекочитаемые подписи для UI (русский, единый источник для бейджей и
 * select-опций в Settings).
 */
export const HEALTH_GROUP_LABELS: Record<HealthGroup, string> = {
  BASIC: 'Основная',
  PREPARATORY: 'Подготовительная',
  SPECIAL_MEDICAL: 'Специальная медицинская',
  AFK: 'АФК',
}

/**
 * Публичный профиль пользователя сайта.
 *
 * Источник: `AuthService.GetMe` через gateway `GET /api/me`.
 * `telegram_chat_id` отсутствует или null, если TG не привязан.
 */
export interface User {
  /** Stringified BIGSERIAL — первичный ключ users. */
  id: string
  /** Нормализованный email (lowercase). */
  email: string
  /** chat_id телеграма; null/undefined если не привязан. */
  telegram_chat_id?: number | null
  /** ISO-8601, момент регистрации. */
  created_at: string
  /** ISO-8601, последний запрос пользователя. */
  last_seen_at: string
}

/**
 * Ответ login/refresh от gateway-svc.
 *
 * Refresh-токен НЕ возвращается в body — он живёт в httpOnly cookie `rt`,
 * недоступной из JS. Это защита от XSS-кражи долгоживущего токена
 * (см. docs/api.md секция 1, docs/review-findings.md #2 resolved 2026-06-02).
 */
export interface AccessToken {
  /** Короткий JWT, TTL 15 мин. */
  access_token: string
  /** ISO-8601, expiry access-токена. */
  access_expires_at: string
  /** ISO-8601, expiry refresh-cookie (для UX «сессия истечёт через…»). */
  refresh_expires_at: string
}

/**
 * Ответ POST /api/stream/ticket — one-time ticket для безопасного открытия SSE.
 *
 * Зачем: EventSource не умеет ставить header'ы; передавать долгоживущий JWT
 * в query небезопасно (попадает в access-логи). Ticket — короткоживущий (5 мин)
 * одноразовый capability-токен. См. docs/api.md секция 6.
 */
export interface StreamTicket {
  /** base64url-encoded ticket. Использовать как `?ticket=<value>` в EventSource. */
  ticket: string
  /** ISO-8601, когда ticket станет невалиден (если не redeem-нут раньше). */
  expires_at: string
}

/**
 * Слот тренировки/занятия.
 *
 * Источник: `common.v1.Slot` + REST-обогащение полем `teacher_rating`
 * (фронту его подкладывает gateway через teachers-svc; в Slot proto его нет).
 */
export interface Slot {
  /** Детерминированный sha1-ключ, стабилен между опросами. */
  id: string
  /** Номер учебной недели LKS, 1..18. */
  week: number
  /** Время в формате "HH:MM-HH:MM" как в LKS. */
  time: string
  /** Название секции, optional в proto. */
  section?: string | null
  /** Корпус/зал. */
  place: string
  /** ФИО преподавателя одной строкой. */
  teacher_name: string
  /** BMSTU UID препода; optional в proto. */
  teacher_uid?: string | null
  /** Рейтинг преподавателя 0..5; REST-обогащение, не из proto Slot. */
  teacher_rating?: number | null
  /** Свободных мест на момент опроса. */
  vacancy: number
  /** UUID семестра LKS. */
  semester_uuid: string
  /** День недели; "ANY" = UNSPECIFIED. */
  day_of_week?: DayOfWeek
}

/**
 * Пользовательский фильтр поиска слотов.
 *
 * Поля singular (`section`, `teacher_uid`) — как в proto.
 *
 * @remarks
 * V2: переезд `section` и `teacher_uid` на массивы (`repeated string`) —
 * см. `docs/wave3-brief.md` пункт 4. Пока singular.
 */
export interface Filter {
  /** UUIDv7 фильтра. */
  id: string
  /** Владелец, FK на users.id. */
  user_id: string
  /** Точное совпадение секции (case-insensitive в filter-svc). null/undefined = любая. */
  section?: string | null
  /** Точное совпадение по UID препода. null/undefined = любой. */
  teacher_uid?: string | null
  /** День недели. "ANY" / undefined = любой. */
  day_of_week?: DayOfWeek
  /** Нижняя граница "HH:MM" (включительно). */
  time_from?: string | null
  /** Верхняя граница "HH:MM" (включительно). */
  time_to?: string | null
  /**
   * Минимальный рейтинг 0..5.
   *
   * @remarks
   * В Wave-3 dis­abl-ится на UI: filter-svc не вызывает teachers-svc,
   * поэтому фильтры с min_rating > 0 фактически не сработают
   * (см. `docs/wave3-brief.md` пункт 2). Coming soon.
   */
  min_rating?: number | null
  /**
   * Минимум свободных мест.
   *
   * @remarks
   * Поле есть в БД, но **не экспонируется** proto-контрактом и DTO gateway-svc.
   * На фронте отображается только при чтении (если backend вернёт), для отправки
   * не используется — gateway вернёт 400 `unknown field min_vacancy` (см. фикс
   * 2026-06-02). Coming soon.
   */
  min_vacancy?: number
  /** Активность; выключенный фильтр сохраняется, но не матчится. */
  enabled: boolean
  /** ISO-8601 создания. */
  created_at?: string
  /** ISO-8601 последнего обновления. */
  updated_at?: string
}

/** Тело запроса создания/обновления фильтра — без серверных полей. */
export type FilterInput = Omit<Filter, 'id' | 'user_id' | 'created_at' | 'updated_at'>

/** Ответ `GET /api/filters`. */
export interface FilterListResponse {
  /** Все фильтры юзера, отсортированы created_at DESC. */
  filters: Filter[]
}

/**
 * Слот, прошедший хотя бы один фильтр.
 *
 * Используется в SSE-событии `new-slot` и потенциально в `GET /api/slots`.
 */
export interface MatchedSlot {
  /** Сам слот. */
  slot: Slot
  /** ID фильтров пользователя, которые матчат этот слот. */
  matched_filter_ids: string[]
  /** True, если это первое алёртабельное появление для юзера. */
  is_new: boolean
}

/** Ответ `GET /api/slots` — снимок последних матчей. */
export interface SlotListResponse {
  /** Слоты, обогащённые matched_filter_ids/is_new. */
  slots: (Slot & Partial<Pick<MatchedSlot, 'matched_filter_ids' | 'is_new'>>)[]
  /** ISO-8601 момент построения снимка. */
  fetched_at: string
}

/**
 * Статус линковки BMSTU-кредов.
 *
 * Источник: `BmstuService.GetStatus` через `GET /api/bmstu/status`.
 */
export interface BmstuStatus {
  /** Статус из enum BmstuLinkStatus. */
  status: BmstuLinkStatus
  /**
   * Выбранная юзером группа здоровья.
   *
   * Отсутствует/пустая, если креды не сохранены (`status === 'NOT_LINKED'`).
   * Бэк опускает поле через `omitempty` в этом случае.
   */
  health_group?: HealthGroup
  /** ISO-8601, последний успешный логин в LKS. */
  last_login_at?: string | null
  /** ISO-8601, expiry текущей сессии p4sess. */
  session_expires_at?: string | null
  /** Текст последней ошибки логина (при INVALID/EXPIRED). */
  last_error?: string | null
}

/** Удобные computed-флаги. */
export interface BmstuStatusFlags {
  /** Креды сохранены (любой не-NOT_LINKED статус). */
  linked: boolean
  /** Последний логин был успешен. */
  valid: boolean
}

/**
 * Тело запроса сохранения BMSTU-кредов.
 *
 * `health_group` обязателен для нового UI (Settings.vue требует выбор группы);
 * если фронт не передаст поле, бэк подставит дефолт `BASIC` для бэквард-совместимости
 * с историческими клиентами (см. `bmstuCredsRequest` в gateway-svc).
 */
export interface BmstuCredentials {
  /** Логин LKS. */
  login: string
  /** Пароль LKS — на бэке шифруется AES-256-GCM. */
  password: string
  /** Выбранная юзером группа здоровья — определяет SEMESTER_UUID в bmstu-svc. */
  health_group: HealthGroup
}

/**
 * Ответ инициации Telegram-линковки.
 *
 * Источник: `AuthService.LinkTelegramInit` → `POST /api/me/telegram/init`.
 */
export interface TelegramLinkInit {
  /** Готовый URL `https://t.me/<bot>?start=<code>` — открыть в новой вкладке. */
  deeplink: string
  /** Сам код для копипасты, если deeplink не сработал. */
  code: string
  /** ISO-8601, когда код истечёт. */
  expires_at: string
}

/**
 * RFC7807 Problem Details — стандартный формат ошибок gateway-svc.
 *
 * Content-Type: `application/problem+json`. См. `docs/api.md` секция «Соглашения по ошибкам».
 */
export interface ProblemDetails {
  /** URI типа проблемы, опционально. */
  type?: string
  /** Краткий человекочитаемый заголовок. */
  title: string
  /** HTTP-статус (дублируется в body). */
  status: number
  /** Подробное описание. */
  detail?: string
  /** UUIDv7 trace для дебага. */
  trace_id?: string
}

/**
 * SSE-событие `new-slot` — приходит из notifier через gateway.
 *
 * payload приходит в data: JSON-парсится; имя события `new-slot`
 * читается через `addEventListener('new-slot', ...)`.
 */
export interface NewSlotEvent {
  /** Сам слот. */
  slot: Slot
  /** ID фильтров, которые сматчились. */
  matched_filter_ids: string[]
}

/** SSE-событие `status` — статусные изменения для юзера (BMSTU INVALID и т.п.). */
export interface StatusEvent {
  /** Дискриминатор: `bmstu_invalid` | `filter_disabled` | ... */
  kind: string
  /** Текст для UI-toast-а. */
  message: string
}
