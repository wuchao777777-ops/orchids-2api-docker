package api

import (
	"net/http"
	"strconv"
	"strings"

	"orchids-api/internal/store"
)

// The bundled admin UI consumes GET /api/models as a bare array, which stays
// the default. An optional `page` or `pageSize` requests a paged envelope.
const (
	defaultAdminModelPageSize = 20
	maxAdminModelPageSize     = 500
)

type adminModelListEnvelope struct {
	Items    []*store.Model `json:"items"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
	Total    int            `json:"total"`
}

// adminModelPaging reads the optional paging query. requested reports whether the
// caller asked for an envelope at all, so the default bare array remains intact.
func adminModelPaging(r *http.Request) (page, pageSize int, requested bool) {
	query := r.URL.Query()
	pageRaw := strings.TrimSpace(query.Get("page"))
	sizeRaw := strings.TrimSpace(query.Get("pageSize"))
	if pageRaw == "" && sizeRaw == "" {
		return 1, defaultAdminModelPageSize, false
	}
	page = 1
	if v, err := strconv.Atoi(pageRaw); err == nil && v > 0 {
		page = v
	}
	pageSize = defaultAdminModelPageSize
	if v, err := strconv.Atoi(sizeRaw); err == nil && v > 0 {
		pageSize = v
	}
	if pageSize > maxAdminModelPageSize {
		pageSize = maxAdminModelPageSize
	}
	return page, pageSize, true
}

// paginateAdminRows slices one page out of rows and reports the total, so an
// envelope's `total` describes everything the filter kept rather than the page.
// The model list uses it to return an empty, non-nil page beyond the end,
// so the envelope still marshals `items` as [].
func paginateAdminRows[T any](rows []T, page, pageSize int) ([]T, int) {
	total := len(rows)
	start := (page - 1) * pageSize
	if start < 0 || start >= total {
		return make([]T, 0), total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return rows[start:end], total
}

func filterAdminModels(models []*store.Model, search string) []*store.Model {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return models
	}
	out := make([]*store.Model, 0, len(models))
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.ModelID), search) ||
			strings.Contains(strings.ToLower(m.Name), search) ||
			strings.Contains(strings.ToLower(m.ID), search) ||
			strings.Contains(strings.ToLower(m.UpstreamModel), search) {
			out = append(out, m)
		}
	}
	return out
}
