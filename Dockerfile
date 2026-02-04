# Используем легкий Python 3.10
FROM python:3.10-slim

# Устанавливаем переменные окружения, чтобы Python не создавал .pyc файлы
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

# Устанавливаем рабочую директорию
WORKDIR /app

# 1. Устанавливаем системные зависимости и Chromium
# chromium-driver — это то, что нужно для selenium
RUN apt-get update && apt-get install -y \
    chromium \
    chromium-driver \
    && rm -rf /var/lib/apt/lists/*

# 2. Копируем файлы зависимостей и устанавливаем их
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 3. Копируем код бота
COPY main.py .

# Запускаем бота
CMD ["python", "main.py"]
