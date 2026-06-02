// Package health — конверторы группы здоровья студента между тремя
// представлениями: protobuf enum (`commonv1.HealthGroup`), строка в БД
// (CHECK-список `BASIC`/`PREPARATORY`/`SPECIAL_MEDICAL`/`AFK`) и dev-friendly
// человекочитаемая подпись.
//
// Зачем выделено в отдельный пакет: и server, и store, и потенциально
// миграции пользуются одной и той же таблицей соответствий. Дублировать
// switch по 4 значениям в трёх местах — приглашение к багу при добавлении
// новой группы.
//
// Соответствие proto↔БД должно совпадать с CHECK-ограничением в миграции
// `00003_health_group.sql`. Любая правка тут — повод проверить миграцию.
package health

import (
	commonv1 "github.com/fizcultor/backend/gen/common/v1"
)

// DB-строки для столбца bmstu_credentials.health_group. Дублируют CHECK
// из миграции 00003.
const (
	DBBasic          = "BASIC"
	DBPreparatory    = "PREPARATORY"
	DBSpecialMedical = "SPECIAL_MEDICAL"
	DBAFK            = "AFK"
)

// DBDefault — то, что подставляется, если юзер прислал UNSPECIFIED.
// Совпадает с DEFAULT 'BASIC' в схеме (бэкворд-совместимость).
const DBDefault = DBBasic

// ProtoToDB конвертирует proto enum в строку для записи в БД.
//
// UNSPECIFIED и любые неизвестные значения отображаются в DBDefault (BASIC) —
// это инвариант, на который опирается StoreCredentials: всегда пишет валидное
// значение, проходящее CHECK.
func ProtoToDB(hg commonv1.HealthGroup) string {
	switch hg {
	case commonv1.HealthGroup_HEALTH_GROUP_BASIC:
		return DBBasic
	case commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY:
		return DBPreparatory
	case commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL:
		return DBSpecialMedical
	case commonv1.HealthGroup_HEALTH_GROUP_AFK:
		return DBAFK
	case commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED:
		return DBDefault
	default:
		return DBDefault
	}
}

// DBToProto конвертирует строку из БД в proto enum.
//
// Неизвестные значения отображаются в UNSPECIFIED. На практике CHECK не даст
// записать что-то кроме 4 валидных значений, но безопасно вернуть
// UNSPECIFIED, а не panic при чтении исторических данных.
func DBToProto(s string) commonv1.HealthGroup {
	switch s {
	case DBBasic:
		return commonv1.HealthGroup_HEALTH_GROUP_BASIC
	case DBPreparatory:
		return commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY
	case DBSpecialMedical:
		return commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL
	case DBAFK:
		return commonv1.HealthGroup_HEALTH_GROUP_AFK
	default:
		return commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED
	}
}
