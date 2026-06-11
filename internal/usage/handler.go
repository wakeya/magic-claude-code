package usage

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Summary(filter Filter) (Summary, error) {
	return h.store.Summary(filter)
}

func (h *Handler) Register(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/usage/summary", wrap(h.handleSummary))
	mux.HandleFunc("/api/usage/trends", wrap(h.handleTrends))
	mux.HandleFunc("/api/usage/requests", wrap(h.handleRequests))
	mux.HandleFunc("/api/usage/providers", wrap(h.handleProviders))
	mux.HandleFunc("/api/usage/models", wrap(h.handleModels))
	mux.HandleFunc("/api/usage/coverage", wrap(h.handleCoverage))
	mux.HandleFunc("/api/usage/clear", wrap(h.handleClear))
}

func (h *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Summary(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleTrends(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Trends(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleRequests(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Requests(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleProviders(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Providers(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Models(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleCoverage(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.parseFilter(w, r)
	if !ok {
		return
	}
	result, err := h.store.Coverage(filter)
	writeUsageJSON(w, result, err)
}

func (h *Handler) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ResetSessionSync bool `json:"reset_session_sync"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}
	}
	result, err := h.store.ClearUsageData(req.ResetSessionSync)
	writeUsageJSON(w, result, err)
}

func (h *Handler) parseFilter(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	q := r.URL.Query()
	tz := q.Get("tz")
	loc := time.Local
	if tz != "" {
		loaded, err := time.LoadLocation(tz)
		if err != nil {
			http.Error(w, `{"error":"invalid tz"}`, http.StatusBadRequest)
			return Filter{}, false
		}
		loc = loaded
	}
	filter := Filter{
		TZ:               tz,
		SourceApp:        q.Get("source_app"),
		SourceEntrypoint: q.Get("source_entrypoint"),
		ProviderID:       q.Get("provider_id"),
		Model:            q.Get("model"),
		Status:           q.Get("status"),
		UsageSource:      q.Get("usage_source"),
		UsageParseStatus: q.Get("usage_parse_status"),
		RequestPath:      q.Get("request_path"),
		Query:            q.Get("q"),
		Page:             parsePositiveInt(q.Get("page")),
		PageSize:         parsePositiveInt(q.Get("page_size")),
		StatsScope:       q.Get("stats_scope"),
	}
	if filter.StatsScope == "" {
		filter.StatsScope = StatsScopeEffective
	}
	if !validStatsScope(filter.StatsScope) {
		http.Error(w, `{"error":"invalid stats_scope"}`, http.StatusBadRequest)
		return Filter{}, false
	}
	var err error
	if filter.From, err = parseFilterTime(q.Get("from"), loc, false); err != nil {
		http.Error(w, `{"error":"invalid from"}`, http.StatusBadRequest)
		return Filter{}, false
	}
	if filter.To, err = parseFilterTime(q.Get("to"), loc, true); err != nil {
		http.Error(w, `{"error":"invalid to"}`, http.StatusBadRequest)
		return Filter{}, false
	}
	return filter, true
}

func validStatsScope(value string) bool {
	switch value {
	case StatsScopeEffective, StatsScopeProvider, StatsScopeSessionLog, StatsScopeRaw:
		return true
	default:
		return false
	}
}

func writeUsageJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		http.Error(w, `{"error":"usage query failed"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func parsePositiveInt(value string) int {
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseFilterTime(value string, loc *time.Location, endOfDate bool) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			if endOfDate {
				t = t.Add(time.Second)
			}
			return t.UTC(), nil
		}
	}
	t, err := time.ParseInLocation("2006-01-02", value, loc)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDate {
		t = t.AddDate(0, 0, 1)
	}
	return t.UTC(), nil
}
