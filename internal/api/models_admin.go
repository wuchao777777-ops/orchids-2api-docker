package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"orchids-api/internal/store"
)

// Admin model listing shapes.
//
// The legacy GET /api/models response is a bare array and the bundled admin UI
// still consumes it that way, so that shape stays the default. grok2api's admin
// frontend expects a paged envelope plus capability groups; both are offered
// additively: pass `page` (or `pageSize`) to get the envelope, and use
// /api/models/groups for the grouped view.
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

// adminModelGroup is one set of routes that share the same endpoint
// capabilities. Key mirrors grok2api's RouteGroup key (the route ids joined with
// ':'); EndpointCapabilities names the capabilities the group serves, which is
// what makes same-name multi-capability routes visible in the admin UI.
type adminModelGroup struct {
	Key                  string         `json:"key"`
	Routes               []*store.Model `json:"routes"`
	EndpointCapabilities []string       `json:"endpointCapabilities"`
}

type adminModelGroupEnvelope struct {
	Items    []adminModelGroup `json:"items"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Total    int               `json:"total"`
}

// adminModelPaging reads the optional paging query. requested reports whether the
// caller asked for an envelope at all, so the legacy bare array can be kept for
// every existing client.
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
// The flat route list and the grouped view share it. A page past the end yields
// an empty, non-nil page, so the envelope still marshals `items` as [].
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

// HandleModelGroups serves GET /api/models/groups: the same routes as
// /api/models, grouped by identical endpoint-capability sets.
func (a *API) HandleModelGroups(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}

	models, err := a.store.ListModels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	models = filterAdminModels(models, r.URL.Query().Get("search"))

	groups := groupAdminModels(models)
	page, pageSize, _ := adminModelPaging(r)
	items, total := paginateAdminRows(groups, page, pageSize)

	writeJSON(w, adminModelGroupEnvelope{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	})
}

// groupAdminModels buckets routes by their capability signature. Routes with no
// declared capabilities cannot be compared, so each becomes its own group.
func groupAdminModels(models []*store.Model) []adminModelGroup {
	type bucket struct {
		caps []string
		rows []*store.Model
	}
	order := make([]string, 0, len(models))
	buckets := make(map[string]*bucket, len(models))
	for _, m := range models {
		caps := append([]string(nil), m.Capabilities...)
		sort.Strings(caps)
		signature := strings.Join(caps, "|")
		if len(caps) == 0 {
			signature = "route:" + m.ID
		}
		b, ok := buckets[signature]
		if !ok {
			b = &bucket{caps: caps}
			buckets[signature] = b
			order = append(order, signature)
		}
		b.rows = append(b.rows, m)
	}

	sort.Strings(order)
	groups := make([]adminModelGroup, 0, len(order))
	for _, signature := range order {
		b := buckets[signature]
		ids := make([]string, 0, len(b.rows))
		for _, row := range b.rows {
			ids = append(ids, row.ID)
		}
		groups = append(groups, adminModelGroup{
			Key:                  strings.Join(ids, ":"),
			Routes:               b.rows,
			EndpointCapabilities: append([]string{}, b.caps...),
		})
	}
	return groups
}
