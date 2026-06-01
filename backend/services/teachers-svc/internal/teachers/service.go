package teachers

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/fizcultor/backend/gen/common/v1"
	teachersv1 "github.com/fizcultor/backend/gen/teachers/v1"
	"github.com/fizcultor/backend/services/teachers-svc/internal/store"
)

// Store — интерфейс БД-слоя, потребляемый Service. Один интерфейс — для
// удобства мокирования в юнит-тестах.
type Store interface {
	GetByUID(ctx context.Context, uid string) (store.Teacher, error)
	BatchGet(ctx context.Context, uids []string) ([]store.Teacher, error)
	List(ctx context.Context, p store.ListParams) ([]store.Teacher, error)
	Count(ctx context.Context) (int64, error)
	Upsert(ctx context.Context, p store.UpsertParams) (store.UpsertResult, error)
}

// Service — реализация teachersv1.TeachersServiceServer.
type Service struct {
	teachersv1.UnimplementedTeachersServiceServer
	store Store
}

// New создаёт Service.
func New(s Store) *Service {
	return &Service{store: s}
}

// Get возвращает одного преподавателя по uid. NOT_FOUND если нет.
func (s *Service) Get(ctx context.Context, req *teachersv1.GetRequest) (*teachersv1.GetResponse, error) {
	uid := strings.TrimSpace(req.GetUid())
	if uid == "" {
		return nil, status.Error(codes.InvalidArgument, "uid is required")
	}
	t, err := s.store.GetByUID(ctx, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "teacher %q not found", uid)
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	return &teachersv1.GetResponse{Teacher: teacherToProto(t)}, nil
}

// BatchGet — пачка по uid; отсутствующие молча пропускаются.
func (s *Service) BatchGet(ctx context.Context, req *teachersv1.BatchGetRequest) (*teachersv1.BatchGetResponse, error) {
	uids := dedupNonEmpty(req.GetUids())
	if len(uids) == 0 {
		return &teachersv1.BatchGetResponse{}, nil
	}
	got, err := s.store.BatchGet(ctx, uids)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "batch get: %v", err)
	}
	out := make([]*teachersv1.Teacher, 0, len(got))
	for _, t := range got {
		out = append(out, teacherToProto(t))
	}
	return &teachersv1.BatchGetResponse{Teachers: out}, nil
}

// List возвращает страницу с фильтрацией.
//
// section_filter в V1 игнорируется (нет такого поля в БД).
// min_rating в V1 игнорируется (фильтрация рейтинга — на стороне UI).
// page.page_token — простой offset как строка, чтобы не плодить сложности.
func (s *Service) List(ctx context.Context, req *teachersv1.ListRequest) (*teachersv1.ListResponse, error) {
	var (
		limit  int32 = 50
		offset int32
	)
	if req.GetPage() != nil {
		if req.GetPage().GetPageSize() > 0 {
			limit = req.GetPage().GetPageSize()
		}
		if t := req.GetPage().GetPageToken(); t != "" {
			n, err := strconv.ParseInt(t, 10, 32)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "bad page_token: %v", err)
			}
			offset = int32(n)
		}
	}

	teachers, err := s.store.List(ctx, store.ListParams{
		NameQuery: req.GetNameQuery(),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list: %v", err)
	}

	out := make([]*teachersv1.Teacher, 0, len(teachers))
	for _, t := range teachers {
		out = append(out, teacherToProto(t))
	}

	resp := &teachersv1.ListResponse{Teachers: out}
	// next_page_token — следующий offset, если страница полная.
	// limit ограничен int32, len(teachers) не может превышать limit, поэтому конверсия безопасна.
	if len(teachers) == int(limit) {
		resp.Page = &commonv1.PageResponse{
			NextPageToken: strconv.FormatInt(int64(offset+limit), 10),
		}
	}
	return resp, nil
}

// Refresh — повторный импорт. inline_json (опц.) приоритетнее embedded.
func (s *Service) Refresh(ctx context.Context, req *teachersv1.RefreshRequest) (*teachersv1.RefreshResponse, error) {
	src := embeddedJSON
	if req.GetInlineJson() != "" {
		src = []byte(req.GetInlineJson())
	}

	stats, err := Import(ctx, s.store, src)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "refresh: %v", err)
	}
	return &teachersv1.RefreshResponse{
		Total:     stats.Total,
		Inserted:  stats.Inserted,
		Updated:   stats.Updated,
		Unchanged: stats.Unchanged,
	}, nil
}

// ImportStats — статистика импорта.
type ImportStats struct {
	// Total — всего записей в источнике.
	Total int32
	// Inserted — новые.
	Inserted int32
	// Updated — обновлённые.
	Updated int32
	// Unchanged — не изменились (в V1 == 0, т.к. always upsert => updated).
	Unchanged int32
}

// Import — общая функция импорта: парс JSON → Upsert на каждый элемент.
// Используется и из Refresh, и из bootstrap.
func Import(ctx context.Context, st Store, data []byte) (ImportStats, error) {
	imported, err := ParseJSON(data)
	if err != nil {
		return ImportStats{}, err
	}
	stats := ImportStats{Total: safeInt32(len(imported))}
	for _, t := range imported {
		res, err := st.Upsert(ctx, store.UpsertParams{
			UID:            t.UID,
			Name:           t.Name,
			NameNormalized: t.NameNormalized,
			Rating:         t.Rating,
		})
		if err != nil {
			return stats, err
		}
		if res.Inserted {
			stats.Inserted++
		} else {
			stats.Updated++
		}
	}
	return stats, nil
}

// teacherToProto конвертирует store.Teacher → teachersv1.Teacher.
func teacherToProto(t store.Teacher) *teachersv1.Teacher {
	p := &teachersv1.Teacher{
		Uid:      t.UID,
		FullName: t.Name,
	}
	if t.Rating != nil {
		p.Rating = *t.Rating
	}
	// reviews_count и sections в V1 не хранятся — оставляем 0/nil.
	return p
}

// safeInt32 ограничивает int значением int32, защищая от переполнения.
// Для статистики приемлемо: если у нас 2^31 учителей, цифру всё равно никто не прочтёт.
func safeInt32(n int) int32 {
	const maxInt32 = (1 << 31) - 1
	if n > maxInt32 {
		return maxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n) // #nosec G115 — после bound-check значение в диапазоне int32.
}

// dedupNonEmpty убирает пустые строки и дубликаты, сохраняя порядок первого появления.
func dedupNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
