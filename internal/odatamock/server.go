// Copyright 2026 Query Farm LLC - https://query.farm

// Package odatamock provides a tiny in-process OData v4 service used by both the
// Go httptest unit tests and the standalone mock server binary (cmd/mockserver)
// that backs the haybarn SQL end-to-end suite.
//
// It serves:
//
//   - GET /                 service document: {"value":[{"name":"People",...}]}
//   - GET /People           entity-set page 1 of 2, with an @odata.nextLink
//   - GET /People?page=2    entity-set page 2 of 2 (no nextLink)
//   - GET /$metadata        an EDMX XML document describing the Person type
//
// The People set returns 4 entities total across two pages. The handler records
// the most recent request's query string so tests can assert that $filter /
// $select / $top were passed through.
package odatamock

import (
	"net/http"
	"net/url"
	"sync"
)

// Server is a mock OData v4 service. Zero value is not usable; call New.
type Server struct {
	mu        sync.Mutex
	lastQuery url.Values // query params of the most recent /People request
}

// New returns a ready-to-serve mock OData server.
func New() *Server { return &Server{} }

// LastQuery returns a copy of the query parameters seen on the most recent
// /People request (used by tests to assert option pass-through).
func (s *Server) LastQuery() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := url.Values{}
	for k, v := range s.lastQuery {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Handler returns an http.Handler serving the mock service. baseURL is the
// absolute URL prefix (scheme://host) used to build the absolute @odata.nextLink;
// pass the test server's URL. If baseURL is empty, a relative nextLink is used.
func (s *Server) Handler(baseURL string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only the exact root path is the service document; everything else 404s.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serviceDoc))
	})

	mux.HandleFunc("/$metadata", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(metadataXML))
	})

	mux.HandleFunc("/People", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(peoplePage2))
			return
		}
		// Record the first-page request's query so tests can assert that the
		// caller's $filter/$select/$top/$orderby were passed through. The
		// follow-up nextLink request (page=2) carries only the next-link params,
		// so recording it would clobber the assertion.
		s.mu.Lock()
		s.lastQuery = r.URL.Query()
		s.mu.Unlock()

		next := baseURL + "/People?page=2"
		_, _ = w.Write([]byte(peoplePage1(next)))
	})

	return mux
}

const serviceDoc = `{
  "@odata.context": "$metadata",
  "value": [
    {"name": "People", "kind": "EntitySet", "url": "People"}
  ]
}`

func peoplePage1(nextLink string) string {
	return `{
  "@odata.context": "$metadata#People",
  "value": [
    {"UserName": "russellwhyte", "FirstName": "Russell", "LastName": "Whyte"},
    {"UserName": "scottketchum", "FirstName": "Scott", "LastName": "Ketchum"}
  ],
  "@odata.nextLink": "` + nextLink + `"
}`
}

const peoplePage2 = `{
  "@odata.context": "$metadata#People",
  "value": [
    {"UserName": "ronaldmundy", "FirstName": "Ronald", "LastName": "Mundy"},
    {"UserName": "javieralfred", "FirstName": "Javier", "LastName": "Alfred"}
  ]
}`

// TotalPeople is the number of People entities across both pages.
const TotalPeople = 4

const metadataXML = `<?xml version="1.0" encoding="utf-8"?>
<edmx:Edmx Version="4.0" xmlns:edmx="http://docs.oasis-open.org/odata/ns/edmx">
  <edmx:DataServices>
    <Schema Namespace="Mock.Models" xmlns="http://docs.oasis-open.org/odata/ns/edm">
      <EntityType Name="Person">
        <Key><PropertyRef Name="UserName"/></Key>
        <Property Name="UserName" Type="Edm.String" Nullable="false"/>
        <Property Name="FirstName" Type="Edm.String"/>
        <Property Name="LastName" Type="Edm.String"/>
      </EntityType>
    </Schema>
  </edmx:DataServices>
</edmx:Edmx>`
