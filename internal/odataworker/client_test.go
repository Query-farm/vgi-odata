// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Query-farm/vgi-odata/internal/odatamock"
)

// newMock starts an httptest server backed by the shared mock OData service and
// returns it along with the mock state (for query assertions).
func newMock(t *testing.T) (*httptest.Server, *odatamock.Server) {
	t.Helper()
	mock := odatamock.New()
	// We need the server's own URL to build absolute nextLinks. httptest assigns
	// the URL only after Start, so wrap the handler to capture *ts.URL lazily.
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.Handler(ts.URL).ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, mock
}

func TestQueryAllPages(t *testing.T) {
	ts, _ := newMock(t)
	got, err := Query(context.Background(), ts.URL, "People", QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != odatamock.TotalPeople {
		t.Fatalf("expected %d entities across both pages, got %d", odatamock.TotalPeople, len(got))
	}
	// seq is contiguous from 0.
	for i, e := range got {
		if e.Seq != int64(i) {
			t.Errorf("entity %d has seq %d", i, e.Seq)
		}
	}
	if !strings.Contains(got[0].Entity, "russellwhyte") {
		t.Errorf("first entity %q missing expected field", got[0].Entity)
	}
	// Last entity comes from page 2.
	if !strings.Contains(got[3].Entity, "javieralfred") {
		t.Errorf("last entity %q not from page 2", got[3].Entity)
	}
}

func TestQueryMaxRowsCap(t *testing.T) {
	ts, _ := newMock(t)
	got, err := Query(context.Background(), ts.URL, "People", QueryOptions{MaxRows: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("max_rows=3 should cap at 3 entities, got %d", len(got))
	}
}

func TestQueryOptionPassThrough(t *testing.T) {
	ts, mock := newMock(t)
	_, err := Query(context.Background(), ts.URL, "People", QueryOptions{
		Filter:  "FirstName eq 'Russell'",
		Select:  "UserName,FirstName",
		Top:     "10",
		OrderBy: "LastName",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	q := mock.LastQuery()
	if got := q.Get("$filter"); got != "FirstName eq 'Russell'" {
		t.Errorf("$filter not passed through: %q", got)
	}
	if got := q.Get("$select"); got != "UserName,FirstName" {
		t.Errorf("$select not passed through: %q", got)
	}
	if got := q.Get("$top"); got != "10" {
		t.Errorf("$top not passed through: %q", got)
	}
	if got := q.Get("$orderby"); got != "LastName" {
		t.Errorf("$orderby not passed through: %q", got)
	}
}

func TestEntitySets(t *testing.T) {
	ts, _ := newMock(t)
	names, err := EntitySets(context.Background(), ts.URL, "")
	if err != nil {
		t.Fatalf("EntitySets: %v", err)
	}
	if len(names) != 1 || names[0] != "People" {
		t.Fatalf("expected [People], got %v", names)
	}
}

func TestMetadata(t *testing.T) {
	ts, _ := newMock(t)
	rows, err := Metadata(context.Background(), ts.URL, "")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 property rows, got %d: %v", len(rows), rows)
	}
	for _, r := range rows {
		if r.EntityType != "Person" {
			t.Errorf("unexpected entity type %q", r.EntityType)
		}
		if r.Type != "Edm.String" {
			t.Errorf("property %q has type %q, want Edm.String", r.Property, r.Type)
		}
	}
	// Spot-check a known property name is present.
	var sawUserName bool
	for _, r := range rows {
		if r.Property == "UserName" {
			sawUserName = true
		}
	}
	if !sawUserName {
		t.Error("UserName property missing from metadata")
	}
}

// --- v2-shape handling -----------------------------------------------------

// TestQueryV2Shape exercises the OData v2 response shape: results under
// d.results and the next link under d.__next.
func TestQueryV2Shape(t *testing.T) {
	page2 := `{"d":{"results":[{"Id":3},{"Id":4}]}}`
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("p") == "2" {
			_, _ = w.Write([]byte(page2))
			return
		}
		next := "http://" + r.Host + "/Products?p=2"
		_, _ = w.Write([]byte(`{"d":{"results":[{"Id":1},{"Id":2}],"__next":"` + next + `"}}`))
	}))
	t.Cleanup(ts.Close)

	got, err := Query(context.Background(), ts.URL, "Products", QueryOptions{Version: "v2"})
	if err != nil {
		t.Fatalf("Query v2: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("v2 paging should yield 4 entities, got %d", len(got))
	}
	if hits != 2 {
		t.Fatalf("expected 2 page requests, got %d", hits)
	}
	if !strings.Contains(got[3].Entity, `"Id":4`) {
		t.Errorf("last v2 entity %q unexpected", got[3].Entity)
	}
}

// --- error cases -----------------------------------------------------------

func TestQuery404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	if _, err := Query(context.Background(), ts.URL, "Nope", QueryOptions{}); err == nil {
		t.Fatal("expected an error for HTTP 404")
	}
}

func TestQuery500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	if _, err := Query(context.Background(), ts.URL, "Things", QueryOptions{}); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
}

func TestQueryBadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	t.Cleanup(ts.Close)
	if _, err := Query(context.Background(), ts.URL, "Things", QueryOptions{}); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestMetadataBadXML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<not<valid<xml"))
	}))
	t.Cleanup(ts.Close)
	if _, err := Metadata(context.Background(), ts.URL, ""); err == nil {
		t.Fatal("expected an error for malformed EDMX XML")
	}
}

func TestQueryBadURL(t *testing.T) {
	if _, err := Query(context.Background(), "not-a-url", "People", QueryOptions{}); err == nil {
		t.Fatal("expected an error for a non-absolute service_url")
	}
}

func TestEntitySetsUnreachable(t *testing.T) {
	// Port 1 is reserved and refuses connections quickly.
	if _, err := EntitySets(context.Background(), "http://127.0.0.1:1/", ""); err == nil {
		t.Fatal("expected an error for an unreachable service")
	}
}
