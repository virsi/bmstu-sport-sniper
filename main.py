import os
import sys
import json
import time
import pickle
import hashlib
import logging
import requests
import threading
import re
from bs4 import BeautifulSoup
from datetime import datetime
from zoneinfo import ZoneInfo
from dotenv import load_dotenv

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from webdriver_manager.chrome import ChromeDriverManager


options = webdriver.ChromeOptions()
options.add_argument("--headless=new")
options.add_argument("--no-sandbox")  # ОБЯЗАТЕЛЬНО для Docker
options.add_argument("--disable-dev-shm-usage")  # ОБЯЗАТЕЛЬНО для Docker
options.add_argument("--disable-gpu")
options.add_argument("--window-size=1920,1080")
# Убираем использование webdriver-manager для поиска бинарника,
# так как мы установили его через apt-get в Dockerfile
options.binary_location = "/usr/bin/chromium"

LAST_UPDATE_ID = 0
LAST_SLOTS_CHECK = 0
SLOTS_CHECK_INTERVAL = 180

RATINGS_URL = "https://studizba.com/hs/mgtu-im-baumana/teachers/fof-1-fizicheskoe-vospitanie/"
BASE_STUDIZBA = "https://studizba.com"
TEACHER_RATINGS = {} # Глобальный кэш

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s [%(levelname)s] %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger(__name__)

basedir = os.path.abspath(os.path.dirname(__file__))
load_dotenv(os.path.join(basedir, '.env'))

# Конфигурация
TELEGRAM_TOKEN = os.getenv("TG_TOKEN")
CHAT_ID = os.getenv("TG_CHAT_ID")
USERNAME = os.getenv("BMSTU_LOGIN")
PASSWORD = os.getenv("BMSTU_PASSWORD")
SEMESTER_UUID = os.getenv("SEMESTER_UUID")

if not all([TELEGRAM_TOKEN, CHAT_ID, USERNAME, PASSWORD, SEMESTER_UUID]):
    logger.critical("Configuration error: Check .env file for missing variables.")
    sys.exit(1)

API_URL = f"https://lks.bmstu.ru/lks-back/api/v1/fv/{SEMESTER_UUID}/groups"
TARGET_URL = "https://lks.bmstu.ru/profile"
COOKIE_DIR = os.path.join(basedir, "cookies")
COOKIE_FILE = os.path.join(COOKIE_DIR, "bmstu_cookies.pkl")

KNOWN_SLOTS = set()


def send_telegram(text, parse_mode=None):
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
    """Выполняет авторизацию через Selenium headless-браузер для обновления сессии."""
    logger.info("Session expired. Initiating re-login via Selenium...")

    options = webdriver.ChromeOptions()
    options.add_argument("--headless=new")
    options.add_argument("--no-sandbox")
    options.add_argument("--disable-dev-shm-usage")
    options.add_argument("--disable-blink-features=AutomationControlled")
    options.add_argument("--window-size=1920,1080")
    options.add_argument("--disable-dev-shm-usage")
    options.binary_location = "/usr/bin/chromium"

    chrome_bin = os.environ.get("CHROME_BIN")
    if chrome_bin:
        options.binary_location = chrome_bin

    system_driver = os.environ.get("CHROMEDRIVER_PATH")
    service = Service(executable_path="/usr/bin/chromedriver")
    driver = None

    try:
        driver = webdriver.Chrome(service=service, options=options)
        driver.get(TARGET_URL)
        wait = WebDriverWait(driver, 25)

        wait.until(EC.visibility_of_element_located((By.ID, "username"))).send_keys(USERNAME)
        driver.find_element(By.ID, "password").send_keys(PASSWORD)
        driver.find_element(By.ID, "kc-login").click()

        # Ожидание редиректа на профиль как признак успеха
        wait.until(EC.url_contains("lks.bmstu.ru/profile"))

        time.sleep(3) # Небольшая пауза для прогрузки cookies
        if not os.path.exists(COOKIE_DIR):
            os.makedirs(COOKIE_DIR)

        with open(COOKIE_FILE, "wb") as f:
            pickle.dump(driver.get_cookies(), f)

        logger.info("Cookies successfully updated.")
    except Exception as e:
        logger.error(f"Selenium login failed: {e}")
    finally:
        if driver:
            driver.quit()


def get_session():
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
            # Если куки плохие, лучше удалить файл
            if os.path.exists(COOKIE_FILE): os.remove(COOKIE_FILE)

    return session


def generate_slot_id(item):
    """Генерирует уникальный ID слота на основе ID API или хеша параметров."""
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
    """Приводит ФИО к формату 'Фамилия И.О.' для сопоставления."""
    if not name: return ""
    # Убираем лишние пробелы и разбиваем
    parts = re.sub(r'\s+', ' ', name.strip()).split()
    if len(parts) >= 3:
        # Иванов Иван Иванович -> Иванов И.И.
        return f"{parts[0]} {parts[1][0]}.{parts[2][0]}."
    elif len(parts) == 2:
        # Иванов Иван -> Иванов И.
        return f"{parts[0]} {parts[1][0]}."
    return name


def fetch_teacher_ratings():
    """Загружает рейтинги из локального JSON-файла."""
    file_path = os.path.join(basedir, 'teachers.json')
    data = {}
    try:
        if os.path.exists(file_path):
            with open(file_path, 'r', encoding='utf-8') as f:
                raw_data = json.load(f)
                for name, info in raw_data.items():
                    # При загрузке сразу делаем ключи и нормализованные имена
                    data[name.lower()] = info
                    data[normalize_name(name).lower()] = info
            logger.info(f"Loaded {len(raw_data)} teachers from JSON.")
        else:
            logger.error("teachers.json not found! Ratings will not be displayed.")
    except Exception as e:
        logger.error(f"Failed to load teachers.json: {e}")
    return data


def find_teacher_info(name):
    """Ищет данные в кэше по полному ФИО или сокращенному."""
    if not name: return None
    name_lower = name.lower()
    norm_name = normalize_name(name).lower()

    # Сначала ищем точное совпадение, потом по инициалам
    return TEACHER_RATINGS.get(name_lower) or TEACHER_RATINGS.get(norm_name)


def format_message(new_items):
    """Формирует читаемое сообщение с учетом рейтинга."""
    msg_lines = ["<b>🔥 ДОСТУПНЫ НОВЫЕ СЛОТЫ!</b>\n"]

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
            # Используем .get('url'), чтобы не упасть, если ссылки нет
            url = t_info.get('url')

            rating_display = f"⭐️ Рейтинг: <b>{rating}</b>"
            if url:
                rating_display += f"\n🔗 <a href='{url}'>Профиль на Studizba</a>"
        else:
            rating_display = "ℹ️ Рейтинг: <i>не найден</i>"

        card = (
            f"🏟 <b>{name}</b>\n"
            f"🗓  {day} |⏰  {time_slot}\n"
            f"📍  {place}\n"
            f"👨‍🏫  {teacher}\n"
            f"{rating_display}\n"
            f"🟢  Свободно мест: <b>{vacancy}</b>"
        )
        msg_lines.append(card)

    return "\n\n".join(msg_lines)


def check_slots():
    global KNOWN_SLOTS
    session = get_session()

    try:
        # 1. Делаем реальный запрос к API
        response = session.get(API_URL, timeout=15)

        # 2. Проверяем авторизацию
        if response.status_code in [401, 403]:
            logger.warning("Access denied (401/403). Token expired.")
            update_cookies_via_selenium()
            return

        # 3. Проверяем общие ошибки сервера
        if response.status_code != 200:
            logger.error(f"API Error: Status {response.status_code}")
            return

        # 4. Получаем данные
        days_list = response.json()

        if not days_list:
            logger.debug("Received empty schedule list.")
            return

        current_slots_map = {}
        new_slots_data = []

        # 5. Парсинг структуры: Список Дней -> Список Групп
        for day_data in days_list:
            groups = day_data.get('groups', [])
            for group in groups:
                slot_id = generate_slot_id(group)
                current_slots_map[slot_id] = group

                vacancy = int(group.get('vacancy', 0))
                if vacancy > 0:
                    # Если слот новый (его ID нет в KNOWN_SLOTS)
                    if slot_id not in KNOWN_SLOTS:
                        new_slots_data.append(group)
                        KNOWN_SLOTS.add(slot_id)

        # 6. Очистка старых ID (чтобы память не росла бесконечно)
        KNOWN_SLOTS.intersection_update(current_slots_map.keys())

        # 7. Отправка уведомлений
        if new_slots_data:
            logger.info(f"Found {len(new_slots_data)} new slots. Sending notification.")
            text = format_message(new_slots_data)
            link = "https://lks.bmstu.ru/fv/new-record"
            full_text = f"{text}\n\n<a href='{link}'><b>ПЕРЕЙТИ К ЗАПИСИ</b></a>"
            send_telegram(full_text, parse_mode="HTML")
        else:
            logger.info("Check completed. No new slots found.")

    except Exception as e:
        logger.error(f"Unexpected error during check: {e}")


def get_all_available_slots():
    """Возвращает список всех доступных для записи слотов (vacancy > 0)."""
    session = get_session()
    slots = []

    try:
        response = session.get(API_URL, timeout=15)

        if response.status_code in [401, 403]:
            logger.warning("Access denied while fetching slots for /start.")
            update_cookies_via_selenium()
            return []

        if response.status_code != 200:
            logger.error(f"API Error while fetching slots: {response.status_code}")
            return []

        days_list = response.json() or []

        for day_data in days_list:
            for group in day_data.get('groups', []):
                if int(group.get('vacancy', 0)) > 0:
                    slots.append(group)

    except Exception as e:
        logger.error(f"Error fetching slots for /start: {e}")

    return slots


def handle_start_command():
    """Обрабатывает команду /start (мгновенный ответ)."""
    logger.info("Processing /start command")
    send_telegram(
        "Привет! Я слежу за свободными местами на физкультуру.\n\n"
        "Чтобы посмотреть список доступных записей прямо сейчас, нажмите /check"
    )


def handle_check_command():
    """Обрабатывает команду /check (запрос актуальных данных)."""
    logger.info("Processing /check command")

    # Можно отправить промежуточное сообщение, чтобы пользователь видел - процесс идет
    # send_telegram("🔍 Проверяю актуальные слоты, подождите...")

    slots = get_all_available_slots()

    if not slots:
        send_telegram("❌ На данный момент доступных записей нет.")
        return

    text = format_message(slots)
    link = "https://lks.bmstu.ru/fv/new-record"
    full_text = f"{text}\n\n<a href='{link}'><b>ПЕРЕЙТИ К ЗАПИСИ</b></a>"
    send_telegram(full_text, parse_mode="HTML")


def check_telegram_commands():
    global LAST_UPDATE_ID
    try:
        response = requests.get(
            f"https://api.telegram.org/bot{TELEGRAM_TOKEN}/getUpdates",
            params={"offset": LAST_UPDATE_ID + 1, "timeout": 1},
            timeout=5
        )
        if response.status_code == 200:
            updates = response.json().get("result", [])
            for update in updates:
                LAST_UPDATE_ID = update["update_id"]
                if "message" in update and "text" in update["message"]:
                    cmd = update["message"]["text"].strip().lower()

                    if cmd == "/start":
                        handle_start_command()
                    elif cmd == "/check":
                        handle_check_command()

    except Exception as e:
        logger.error(f"Error in commands thread: {e}")


def telegram_loop():
    """Бесконечный цикл для мгновенной обработки команд"""
    logger.info("Telegram command listener started.")
    while True:
        check_telegram_commands()
        time.sleep(0.5) # Минимальная пауза, чтобы не спамить CPU


def main():
    global TEACHER_RATINGS
    # 1. Сначала запускаем поток команд, чтобы /check работал сразу
    threading.Thread(target=telegram_loop, daemon=True).start()

    # 2. Парсим рейтинги
    try:
        TEACHER_RATINGS = fetch_teacher_ratings()
    except:
        send_telegram("⚠️ Рейтинги не загружены.")

    # 3. Проверка куки
    if not os.path.exists(COOKIE_FILE):
        try:
            update_cookies_via_selenium()
        except Exception as e:
            logger.error(f"Critical error during initial login: {e}")
            # Не останавливаем бота, чтобы /check работал, но логируем

    # 4. Основной цикл
    while True:
        check_slots()
        time.sleep(SLOTS_CHECK_INTERVAL)


if __name__ == "__main__":
    main()
