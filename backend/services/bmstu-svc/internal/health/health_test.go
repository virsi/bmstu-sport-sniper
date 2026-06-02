package health_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	"github.com/fizcultor/backend/services/bmstu-svc/internal/health"
)

func TestProtoToDB(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   commonv1.HealthGroup
		want string
	}{
		{commonv1.HealthGroup_HEALTH_GROUP_BASIC, health.DBBasic},
		{commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY, health.DBPreparatory},
		{commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL, health.DBSpecialMedical},
		{commonv1.HealthGroup_HEALTH_GROUP_AFK, health.DBAFK},
		{commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED, health.DBDefault},
		{commonv1.HealthGroup(999), health.DBDefault},
	}
	for _, c := range cases {
		require.Equal(t, c.want, health.ProtoToDB(c.in), "in=%v", c.in)
	}
}

func TestDBToProto(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want commonv1.HealthGroup
	}{
		{health.DBBasic, commonv1.HealthGroup_HEALTH_GROUP_BASIC},
		{health.DBPreparatory, commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY},
		{health.DBSpecialMedical, commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL},
		{health.DBAFK, commonv1.HealthGroup_HEALTH_GROUP_AFK},
		{"", commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED},
		{"INVALID", commonv1.HealthGroup_HEALTH_GROUP_UNSPECIFIED},
	}
	for _, c := range cases {
		require.Equal(t, c.want, health.DBToProto(c.in), "in=%q", c.in)
	}
}

// Roundtrip защищает инвариант ProtoToDB → DBToProto = identity для всех
// валидных enum значений (кроме UNSPECIFIED — он normalises на BASIC).
func TestRoundtrip(t *testing.T) {
	t.Parallel()

	valid := []commonv1.HealthGroup{
		commonv1.HealthGroup_HEALTH_GROUP_BASIC,
		commonv1.HealthGroup_HEALTH_GROUP_PREPARATORY,
		commonv1.HealthGroup_HEALTH_GROUP_SPECIAL_MEDICAL,
		commonv1.HealthGroup_HEALTH_GROUP_AFK,
	}
	for _, hg := range valid {
		require.Equal(t, hg, health.DBToProto(health.ProtoToDB(hg)), "hg=%v", hg)
	}
}
