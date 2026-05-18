package usage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUsageSummaryHandler(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "summary-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 3, OutputTokens: 2})

	rec := serveUsageRequest(store, "/api/usage/summary?tz=UTC")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if got.ProviderRequestsTotal != 1 || got.TokenConsumptionTotal != 5 || got.UsageCoverage != 1 {
		t.Fatalf("summary = %#v", got)
	}
}

func TestUsageRequestsHandlerFiltersAndSearches(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "cli-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})
	req := testUsageRequest("vscode-1", started.Add(time.Minute))
	req.SourceEntrypoint = "claude-vscode"
	req.ProviderName = "Searchable Provider"
	if err := store.Record(req, TokenRecord{RequestID: req.ID, UsageSource: UsageSourceNone, UsageParseStatus: ParseStatusMissing}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	rec := serveUsageRequest(store, "/api/usage/requests?source_entrypoint=claude-vscode&usage_source=none&q=Searchable&page=1&page_size=10")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got RequestPage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode requests: %v", err)
	}
	if got.Total != 1 || len(got.Rows) != 1 || got.Rows[0].ID != "vscode-1" {
		t.Fatalf("page = %#v", got)
	}
}

func TestUsageCoverageHandler(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "coverage-handler-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})

	rec := serveUsageRequest(store, "/api/usage/coverage")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got []CoverageRow
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if len(got) != 1 || got[0].ProviderName != "Provider A" {
		t.Fatalf("coverage = %#v", got)
	}
}

func TestUsageHandlersRejectInvalidTimezone(t *testing.T) {
	store := newTestStore(t)

	rec := serveUsageRequest(store, "/api/usage/summary?tz=Not/AZone")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func serveUsageRequest(store *Store, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	NewHandler(store).Register(mux, func(next http.HandlerFunc) http.HandlerFunc { return next })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	mux.ServeHTTP(rec, req)
	return rec
}
