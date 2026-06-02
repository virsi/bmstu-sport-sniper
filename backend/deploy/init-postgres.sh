#!/bin/sh
# Создаёт per-service БД при первом запуске postgres-контейнера.
# Запускается через docker-entrypoint-initdb.d.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "${POSTGRES_DB:-postgres}" <<-EOSQL
    CREATE DATABASE auth_db;
    CREATE DATABASE bmstu_db;
    CREATE DATABASE filter_db;
    CREATE DATABASE teachers_db;
EOSQL
