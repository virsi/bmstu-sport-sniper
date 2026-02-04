import requests
import time
import json
import os
import pickle
import sys
import hashlib
from datetime import datetime
from dotenv import load_dotenv
from zoneinfo import ZoneInfo
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from webdriver_manager.chrome import ChromeDriverManager

# --- КОНФИГУРАЦИЯ ---
basedir = os.path.abspath(os.path.dirname(__file__))
load_dotenv(os.path.join(basedir, '.env'))

TELEGRAM_TOKEN = os.getenv("TG_TOKEN")
CHAT_ID = os.getenv("TG_CHAT_ID")
USERNAME = os.getenv("BMSTU_LOGIN")
PASSWORD = os.getenv("BMSTU_PASSWORD")
SEMESTER_UUID = os.getenv("SEMESTER_UUID")

if not all([TELEGRAM_TOKEN, CHAT_ID, USERNAME, PASSWORD, SEMESTER_UUID]):
    print("❌ ОШИБКА: Проверь .env!")
    sys.exit(1)

API_URL = f"https://lks.bmstu.ru/lks-back/api/v1/fv/{SEMESTER_UUID}/groups"
TARGET_URL = "https://lks.bmstu.ru/profile"
COOKIE_DIR = os.path.join(basedir, "cookies")
COOKIE_FILE = os.path.join(COOKIE_DIR, "bmstu_cookies.pkl")

# Глобальная память для ID слотов
KNOWN_SLOTS = set()

def send_telegram(text, parse_mode=None):
    try:
        data = {"chat_id": CHAT_ID, "text": text}
        if parse_mode:
            data["parse_mode"] = parse_mode
            data["disable_web_page_preview"] = "true"

        requests.post(
            f"https://api.telegram.org/bot{TELEGRAM_TOKEN}/sendMessage",
            data=data, timeout=10
        )
    except Exception as e:
        print(f"Ошибка TG: {e}")

def update_cookies_via_selenium():
    """Логин через Selenium"""
    print("🔄 Запускаю обновление кук...")
    options = webdriver.ChromeOptions()
    options.add_argument("--headless=new")
    options.add_argument("--no-sandbox")
    options.add_argument("--disable-dev-shm-usage")
    options.add_argument("--disable-blink-features=AutomationControlled")
    options.add_argument("--window-size=1920,1080")

    chrome_bin = os.environ.get("CHROME_BIN")
    if chrome_bin: options.binary_location = chrome_bin

    system_driver = os.environ.get("CHROMEDRIVER_PATH")
    if system_driver and os.path.exists(system_driver):
        service = Service(system_driver)
    else:
        service = Service(ChromeDriverManager().install())

    driver = None
    try:
        driver = webdriver.Chrome(service=service, options=options)
        driver.get(TARGET_URL)
        wait = WebDriverWait(driver, 25)

        wait.until(EC.visibility_of_element_located((By.ID, "username"))).send_keys(USERNAME)
        driver.find_element(By.ID, "password").send_keys(PASSWORD)
        driver.find_element(By.ID, "kc-login").click()
        wait.until(EC.url_contains("lks.bmstu.ru/profile"))

        time.sleep(3)
        if not os.path.exists(COOKIE_DIR): os.makedirs(COOKIE_DIR)
        pickle.dump(driver.get_cookies(), open(COOKIE_FILE, "wb"))
        print("✅ Куки обновлены!")
    except Exception as e:
        print(f"❌ Ошибка Selenium: {e}")
    finally:
        if driver: driver.quit()

def get_session():
    session = requests.Session()
    session.headers.update({
        'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
    })
    if os.path.exists(COOKIE_FILE):
        try:
            with open(COOKIE_FILE, "rb") as f:
                for cookie in pickle.load(f):
                    session.cookies.set(cookie['name'], cookie['value'])
        except: pass
    return session

def generate_slot_id(item):
    """Используем ID из JSON, он там есть и выглядит надежно"""
    if item.get('id'):
        return str(item.get('id'))

    # Фолбек хеш (на всякий случай)
    parts = [
        str(item.get('week', '')),
        str(item.get('time', '')),
        str(item.get('teacherUid', '')),
        str(item.get('section', ''))
    ]
    return hashlib.md5("_".join(parts).encode()).hexdigest()

def format_message(new_items):
    msg_lines = ["🔥 <b>НАЙДЕНЫ НОВЫЕ ЗАПИСИ!</b>\n"]

    for item in new_items:
        # Парсим по твоей структуре JSON
        name = item.get('section') or "Тренировка"
        day = item.get('week') or "День недели"
        time_slot = item.get('time') or "??"
        place = item.get('place') or "СК МГТУ"
        teacher = item.get('teacherName') or ""
        vacancy = item.get('vacancy', 0)

        card = f"🏟 <b>{name}</b>"
        card += f"\n🗓 <b>{day}</b> | ⏰ <b>{time_slot}</b>"
        if place: card += f"\n📍 {place}"
        if teacher: card += f"\n👨‍🏫 {teacher}"

        # Добавляем инфо о местах (зеленый кружок, если много мест)
        vac_icon = "🟢" if int(vacancy) > 5 else "🔴"
        card += f"\n{vac_icon} Мест свободно: <b>{vacancy}</b>"

        msg_lines.append(card)
        msg_lines.append("───────────────")

    return "\n".join(msg_lines)

def check_slots():
    global KNOWN_SLOTS
    session = get_session()

    try:
        now = datetime.now().strftime('%H:%M:%S')
        print(f"[{now}] Проверка...", end=" ")

        response = session.get(API_URL, timeout=15)

        if response.status_code in [401, 403]:
            print("🔐 Куки истекли.")
            update_cookies_via_selenium()
            return

        if response.status_code != 200:
            print(f"Ошибка API: {response.status_code}")
            return

        # Данные приходят как список дней: [{weekDay: "Пн", groups: [...]}, ...]
        days_list = response.json()

        if not days_list:
            print("Пусто (нет списка дней).")
            return

        current_slots_map = {}
        new_slots_data = []

        # ДВОЙНОЙ ЦИКЛ: Идем по дням, потом по группам внутри дня
        for day_data in days_list:
            groups = day_data.get('groups', [])

            for group in groups:
                # Теперь работаем с конкретным занятием
                slot_id = generate_slot_id(group)
                current_slots_map[slot_id] = group

                # Фильтр: есть ли места? (vacancy > 0)
                # И новый ли это слот?
                if int(group.get('vacancy', 0)) > 0:
                    if slot_id not in KNOWN_SLOTS:
                        new_slots_data.append(group)
                        KNOWN_SLOTS.add(slot_id)

        # Чистим память (удаляем те, что пропали из выдачи)
        KNOWN_SLOTS.intersection_update(current_slots_map.keys())

        if new_slots_data:
            print(f"⚡️ Найдено: {len(new_slots_data)}")
            text = format_message(new_slots_data)
            link = "https://lks.bmstu.ru/fv/new-record"
            full_text = f"{text}\n\n👉 <a href='{link}'><b>ЗАПИСАТЬСЯ</b></a>"
            send_telegram(full_text, parse_mode="HTML")
        else:
            print("Новых слотов нет.")

    except Exception as e:
        print(f"\n❌ Ошибка: {e}")

def main():
    print("🚀 Снайпер запущен (Режим: Вложенный JSON)")
    send_telegram("Бот обновлен и готов к охоте!")

    if not os.path.exists(COOKIE_FILE):
        update_cookies_via_selenium()

    while True:
        check_slots()
        time.sleep(45)

if __name__ == "__main__":
    main()
