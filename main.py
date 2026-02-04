import os
import sys
import json
import time
import pickle
import hashlib
import logging
import threading
import re
import requests
from dotenv import load_dotenv

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC

# --- Конфигурация Логирования ---
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s [%(levelname)s] %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger(__name__)

# --- Загрузка окружения ---
BASE_DIR = os.path.abspath(os.path.dirname(__file__))
load_dotenv(os.path.join(BASE_DIR, '.env'))

# Переменные окружения
TELEGRAM_TOKEN = os.getenv("TG_TOKEN")
CHAT_ID = os.getenv("TG_CHAT_ID")
USERNAME = os.getenv("BMSTU_LOGIN")
PASSWORD = os.getenv("BMSTU_PASSWORD")
SEMESTER_UUID = os.getenv("SEMESTER_UUID")

# Пути к драйверам (из Docker Compose)
CHROME_BIN = os.getenv("CHROME_BIN", "/usr/bin/chromium")
CHROMEDRIVER_PATH = os.getenv("CHROMEDRIVER_PATH", "/usr/bin/chromedriver")

if not all([TELEGRAM_TOKEN, CHAT_ID, USERNAME, PASSWORD, SEMESTER_UUID]):
    logger.critical("Configuration error: Check .env file for missing variables.")
    sys.exit(1)

# --- Константы ---
API_URL = f"https://lks.bmstu.ru/lks-back/api/v1/fv/{SEMESTER_UUID}/groups"
TARGET_URL = "https://lks.bmstu.ru/profile"
COOKIE_DIR = os.path.join(BASE_DIR, "cookies")
COOKIE_FILE = os.path.join(COOKIE_DIR, "bmstu_cookies.pkl")
TEACHERS_FILE = os.path.join(BASE_DIR, 'teachers.json')

SLOTS_CHECK_INTERVAL = 180  # Интервал проверки слотов (сек)

# Глобальные переменные состояния
LAST_UPDATE_ID = 0
KNOWN_SLOTS = set()
TEACHER_RATINGS = {}


def send_telegram(text, parse_mode=None):
    """Отправляет сообщение в Telegram."""
    try:
        data = {"chat_id": CHAT_ID, "text": text}
        if parse_mode:
            data["parse_mode"] = parse_mode
            data["disable_web_page_preview"] = "true"

        response = requests.post(
            f"https://api.telegram.org/bot{TELEGRAM_TOKEN}/sendMessage",
            data=data, timeout=10
        )
        response.raise_for_status()
    except Exception as e:
        logger.error(f"Failed to send Telegram message: {e}")


def update_cookies_via_selenium():
    """
    Запускает headless-браузер, логинится в ЛКС и сохраняет cookies.
    Использует настройки путей из переменных окружения (для Docker).
    """
    logger.info("Session expired. Initiating re-login via Selenium...")

    options = webdriver.ChromeOptions()
    options.binary_location = CHROME_BIN
    options.add_argument("--headless=new")
    options.add_argument("--no-sandbox")
    options.add_argument("--disable-dev-shm-usage")
    options.add_argument("--disable-gpu")
    options.add_argument("--window-size=1920,1080")
    options.add_argument("--disable-blink-features=AutomationControlled")

    service = Service(executable_path=CHROMEDRIVER_PATH)
    driver = None

    try:
        driver = webdriver.Chrome(service=service, options=options)
        driver.get(TARGET_URL)
        wait = WebDriverWait(driver, 25)

        # Авторизация
        wait.until(EC.visibility_of_element_located((By.ID, "username"))).send_keys(USERNAME)
        driver.find_element(By.ID, "password").send_keys(PASSWORD)
        driver.find_element(By.ID, "kc-login").click()

        # Ждем редиректа как подтверждения входа
        wait.until(EC.url_contains("lks.bmstu.ru/profile"))

        # Сохраняем куки
        if not os.path.exists(COOKIE_DIR):
            os.makedirs(COOKIE_DIR)

        with open(COOKIE_FILE, "wb") as f:
            pickle.dump(driver.get_cookies(), f)

        logger.info("Cookies successfully updated.")

    except Exception as e:
        logger.error(f"Selenium login failed: {e}")
        raise e  # Пробрасываем ошибку выше
    finally:
        if driver:
            driver.quit()


def get_session():
    """Создает сессию requests с загруженными куками."""
    session = requests.Session()
    session.headers.update({
        'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
    })

    if os.path.exists(COOKIE_FILE):
        try:
            with open(COOKIE_FILE, "rb") as f:
                cookies = pickle.load(f)
                for cookie in cookies:
                    session.cookies.set(cookie['name'], cookie['value'])
        except Exception as e:
            logger.warning(f"Could not load cookies: {e}")
            # Удаляем битый файл
            try:
                os.remove(COOKIE_FILE)
            except OSError:
                pass

    return session


def generate_slot_id(item):
    """Генерирует уникальный хеш для слота, чтобы отличать новые от старых."""
    if item.get('id'):
        return str(item.get('id'))

    parts = [
        str(item.get('week', '')),
        str(item.get('time', '')),
        str(item.get('teacherUid', '')),
        str(item.get('section', ''))
    ]
    return hashlib.md5("_".join(parts).encode()).hexdigest()


def normalize_name(name):
    """
    Приводит ФИО к формату 'Фамилия И.О.' для поиска в базе рейтингов.
    Пример: 'Иванов Иван Иванович' -> 'Иванов И.И.'
    """
    if not name:
        return ""
    # Убираем лишние пробелы и разбиваем
    parts = re.sub(r'\s+', ' ', name.strip()).split()
    if len(parts) >= 3:
        return f"{parts[0]} {parts[1][0]}.{parts[2][0]}."
    elif len(parts) == 2:
        return f"{parts[0]} {parts[1][0]}."
    return name


def load_teacher_ratings():
    """Загружает базу преподавателей из JSON."""
    data = {}
    try:
        if os.path.exists(TEACHERS_FILE):
            with open(TEACHERS_FILE, 'r', encoding='utf-8') as f:
                raw_data = json.load(f)
                for name, info in raw_data.items():
                    # Сохраняем и полное имя, и нормализованное для гибкости поиска
                    data[name.lower()] = info
                    data[normalize_name(name).lower()] = info
            logger.info(f"Loaded {len(raw_data)} teachers from JSON.")
        else:
            logger.warning("teachers.json not found! Ratings will be unavailable.")
    except Exception as e:
        logger.error(f"Failed to load teachers.json: {e}")
    return data


def find_teacher_info(name):
    """Ищет преподавателя в кэше рейтингов."""
    if not name:
        return None
    name_lower = name.lower()
    norm_name = normalize_name(name).lower()
    return TEACHER_RATINGS.get(name_lower) or TEACHER_RATINGS.get(norm_name)


def format_message(new_items, title="🔥 ДОСТУПНЫ НОВЫЕ СЛОТЫ!"):
    """Формирует HTML-сообщение для Telegram."""
    msg_lines = [f"<b>{title}</b>\n"]

    for item in new_items:
        name = item.get('section') or "Тренировка"
        day = item.get('week') or "День недели"
        time_slot = item.get('time') or "??"
        place = item.get('place') or "СК МГТУ"
        teacher = item.get('teacherName') or "Преподаватель не указан"
        vacancy = item.get('vacancy', 0)

        # Поиск рейтинга
        t_info = find_teacher_info(teacher)
        if t_info:
            rating = t_info.get('rating', '??')
            url = t_info.get('url')
            rating_display = f"⭐️ Рейтинг: <b>{rating}</b>"
            if url:
                rating_display += f" (<a href='{url}'>Studizba</a>)"
        else:
            rating_display = "ℹ️ Рейтинг: <i>не найден</i>"

        card = (
            f"🏟 <b>{name}</b>\n"
            f"🗓 {day} | ⏰ {time_slot}\n"
            f"📍 {place}\n"
            f"👨‍🏫 {teacher}\n"
            f"{rating_display}\n"
            f"🟢 Свободно мест: <b>{vacancy}</b>"
        )
        msg_lines.append(card)

    return "\n\n".join(msg_lines)


def get_all_available_slots():
    """Делает запрос к API для получения ВСЕХ текущих слотов (для команды /check)."""
    session = get_session()
    slots = []
    try:
        response = session.get(API_URL, timeout=15)

        # Если 401, пробуем обновить токен, но не рекурсивно, чтобы не зависнуть
        if response.status_code in [401, 403]:
            logger.warning("Token expired during /check command.")
            try:
                update_cookies_via_selenium()
                # Повторный запрос с новой сессией
                session = get_session()
                response = session.get(API_URL, timeout=15)
            except Exception:
                return []

        if response.status_code == 200:
            days_list = response.json() or []
            for day_data in days_list:
                for group in day_data.get('groups', []):
                    if int(group.get('vacancy', 0)) > 0:
                        slots.append(group)
    except Exception as e:
        logger.error(f"Error fetching slots manual check: {e}")

    return slots


def check_slots_job():
    """Основная периодическая задача проверки слотов."""
    global KNOWN_SLOTS
    session = get_session()

    try:
        response = session.get(API_URL, timeout=15)

        if response.status_code in [401, 403]:
            logger.warning("Access denied (401/403). Updating cookies...")
            update_cookies_via_selenium()
            return

        if response.status_code != 200:
            logger.error(f"API Error: Status {response.status_code}")
            return

        days_list = response.json()
        if not days_list:
            return

        current_slots_map = {}
        new_slots_data = []

        for day_data in days_list:
            groups = day_data.get('groups', [])
            for group in groups:
                slot_id = generate_slot_id(group)
                current_slots_map[slot_id] = group

                if int(group.get('vacancy', 0)) > 0:
                    # Если слот видим впервые
                    if slot_id not in KNOWN_SLOTS:
                        new_slots_data.append(group)
                        KNOWN_SLOTS.add(slot_id)

        # Garbage Collector: удаляем из памяти ID слотов, которые исчезли из расписания
        KNOWN_SLOTS.intersection_update(current_slots_map.keys())

        if new_slots_data:
            logger.info(f"New slots found: {len(new_slots_data)}")
            text = format_message(new_slots_data)
            link = "https://lks.bmstu.ru/fv/new-record"
            full_text = f"{text}\n\n<a href='{link}'><b>✍️ ЗАПИСАТЬСЯ</b></a>"
            send_telegram(full_text, parse_mode="HTML")
        else:
            logger.debug("No new slots.")

    except Exception as e:
        logger.error(f"Job error: {e}")


# --- Обработчики Telegram ---

def handle_check_command():
    """Обработка команды /check."""
    logger.info("Command /check received.")
    slots = get_all_available_slots()

    if not slots:
        send_telegram("❌ Доступных мест пока нет.")
        return

    # Если слотов слишком много, Telegram может не пропустить одно сообщение (лимит 4096 символов)
    # Берем первые 10 для безопасности
    text = format_message(slots[:10], title="🔍 АКТУАЛЬНЫЕ СЛОТЫ (Топ-10)")
    link = "https://lks.bmstu.ru/fv/new-record"

    if len(slots) > 10:
        text += f"\n\n<i>...и еще {len(slots)-10} слотов.</i>"

    full_text = f"{text}\n\n<a href='{link}'><b>✍️ ЗАПИСАТЬСЯ</b></a>"
    send_telegram(full_text, parse_mode="HTML")


def telegram_poller():
    """Поток для прослушивания команд Telegram (Long Polling вручную)."""
    global LAST_UPDATE_ID
    logger.info("Telegram listener started.")

    while True:
        try:
            # Делаем запрос к API Telegram
            response = requests.get(
                f"https://api.telegram.org/bot{TELEGRAM_TOKEN}/getUpdates",
                params={"offset": LAST_UPDATE_ID + 1, "timeout": 30}, # Long polling 30 сек
                timeout=35
            )

            if response.status_code == 200:
                result = response.json().get("result", [])
                for update in result:
                    LAST_UPDATE_ID = update["update_id"]

                    if "message" in update and "text" in update["message"]:
                        chat_id = str(update["message"]["chat"]["id"])
                        text = update["message"]["text"].strip().lower()

                        # Реагируем только на сообщения из нужного чата
                        if chat_id == CHAT_ID:
                            if text == "/start":
                                send_telegram("👋 Привет! Я бот для физры.\nЖми /check для проверки мест.")
                            elif text == "/check":
                                handle_check_command()
        except Exception as e:
            logger.error(f"Telegram polling error: {e}")
            time.sleep(5) # Пауза перед ретраем при ошибке сети

        time.sleep(0.5)


def main():
    global TEACHER_RATINGS

    # 1. Загружаем рейтинги
    TEACHER_RATINGS = load_teacher_ratings()

    # 2. Проверяем наличие кук, если нет - создаем
    if not os.path.exists(COOKIE_FILE):
        try:
            update_cookies_via_selenium()
        except Exception:
            logger.error("Initial login failed. Bot will retry later.")

    # 3. Запускаем Telegram-бота в отдельном потоке (daemon=True, чтобы закрылся вместе с основным)
    tg_thread = threading.Thread(target=telegram_poller, daemon=True)
    tg_thread.start()

    logger.info("Main loop started.")

    # 4. Основной цикл проверки слотов
    while True:
        check_slots_job()
        time.sleep(SLOTS_CHECK_INTERVAL)

if __name__ == "__main__":
    main()
