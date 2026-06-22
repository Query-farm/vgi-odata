// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CatalogName is the VGI catalog name advertised by this worker.
const CatalogName = "odata"

// defaultMaxRows caps the number of entities returned by a paged query so a
// runaway service (or a buggy nextLink) can never make the worker loop forever.
const defaultMaxRows = 10000

// defaultTimeout bounds every individual HTTP request so an unreachable or slow
// service fails fast instead of hanging.
const defaultTimeout = 30 * time.Second

// QueryOptions carries the OData system query options and transport settings
// shared by odata_query. Empty string fields are treated as "not set".
type QueryOptions struct {
	Filter  string // $filter
	Select  string // $select
	OrderBy string // $orderby
	Top     string // $top (raw, passed through verbatim)
	MaxRows int64  // paging cap (total entities collected); <=0 → defaultMaxRows
	Token   string // Bearer token (Authorization: Bearer <token>)
	Version string // "v2" or "v4" (response-shape selector); default "v4"
}

// Entity is a single OData entity returned as its raw JSON object text.
type Entity struct {
	Seq    int64
	Entity string
}

// httpClient is the package HTTP client. It is a var so tests can point it at a
// shorter-timeout / instrumented client if needed.
var httpClient = &http.Client{Timeout: defaultTimeout}

// doGET issues a GET to rawURL with an optional Bearer token and returns the
// decoded body, surfacing a clear error for non-2xx responses.
func doGET(ctx context.Context, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("odata: build request for %q: %w", rawURL, err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("odata: GET %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("odata: read body from %q: %w", rawURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("odata: GET %q: HTTP %d: %s", rawURL, resp.StatusCode, snippet)
	}
	return body, nil
}

// joinURL appends entitySet as a path segment to serviceURL, preserving any
// existing path on the service root.
func joinURL(serviceURL, entitySet string) (string, error) {
	u, err := url.Parse(serviceURL)
	if err != nil {
		return "", fmt.Errorf("odata: invalid service_url %q: %w", serviceURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("odata: service_url %q must be an absolute http(s) URL", serviceURL)
	}
	base := strings.TrimRight(u.Path, "/")
	u.Path = base + "/" + strings.TrimLeft(entitySet, "/")
	return u.String(), nil
}

// applyQueryOptions returns rawURL with the OData $-options applied. Existing
// query parameters on rawURL are preserved.
func applyQueryOptions(rawURL string, opts QueryOptions) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("odata: invalid url %q: %w", rawURL, err)
	}
	q := u.Query()
	if opts.Filter != "" {
		q.Set("$filter", opts.Filter)
	}
	if opts.Select != "" {
		q.Set("$select", opts.Select)
	}
	if opts.OrderBy != "" {
		q.Set("$orderby", opts.OrderBy)
	}
	if opts.Top != "" {
		q.Set("$top", opts.Top)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// v4Page is the relevant shape of an OData v4 entity-set response.
type v4Page struct {
	Value    []json.RawMessage `json:"value"`
	NextLink string            `json:"@odata.nextLink"`
}

// v2Page is the relevant shape of an OData v2 entity-set response. v2 nests its
// payload under "d", with results under "d.results" (collection) and the next
// link under "d.__next".
type v2Page struct {
	D struct {
		Results []json.RawMessage `json:"results"`
		Next    string            `json:"__next"`
	} `json:"d"`
}

// v2SingleD covers the v2 case where "d" is itself the collection array rather
// than an object with a "results" field (some v2 services emit `{"d":[...]}`).
type v2SingleD struct {
	D []json.RawMessage `json:"d"`
}

// Query fetches entities from {serviceURL}/{entitySet}, following nextLink
// paging to completion or until opts.MaxRows entities have been collected.
func Query(ctx context.Context, serviceURL, entitySet string, opts QueryOptions) ([]Entity, error) {
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	first, err := joinURL(serviceURL, entitySet)
	if err != nil {
		return nil, err
	}
	next, err := applyQueryOptions(first, opts)
	if err != nil {
		return nil, err
	}

	v2 := strings.EqualFold(opts.Version, "v2")
	out := make([]Entity, 0, 64)
	var seq int64
	// pageCap bounds the number of HTTP requests independently of maxRows, so a
	// service that returns a non-advancing nextLink with empty pages still
	// terminates.
	const pageCap = 100000
	for page := 0; next != "" && int64(len(out)) < maxRows; page++ {
		if page >= pageCap {
			return nil, fmt.Errorf("odata: paging exceeded %d requests; aborting (possible nextLink loop)", pageCap)
		}
		body, err := doGET(ctx, next, opts.Token)
		if err != nil {
			return nil, err
		}
		items, nextLink, err := decodePage(body, v2)
		if err != nil {
			return nil, fmt.Errorf("odata: decode page from %q: %w", next, err)
		}
		for _, raw := range items {
			if int64(len(out)) >= maxRows {
				break
			}
			out = append(out, Entity{Seq: seq, Entity: string(raw)})
			seq++
		}
		next = resolveNext(next, nextLink)
	}
	return out, nil
}

// decodePage parses one entity-set response page for the given (v2/v4) shape,
// returning the entity objects and the next-link (empty when none).
func decodePage(body []byte, v2 bool) ([]json.RawMessage, string, error) {
	if v2 {
		var p v2Page
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, "", err
		}
		if p.D.Results != nil {
			return p.D.Results, p.D.Next, nil
		}
		// Fall back to the `{"d":[...]}` form.
		var s v2SingleD
		if err := json.Unmarshal(body, &s); err != nil {
			return nil, "", err
		}
		return s.D, "", nil
	}
	var p v4Page
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "", err
	}
	return p.Value, p.NextLink, nil
}

// resolveNext resolves a (possibly relative) next-link against the URL of the
// page it was found on. An empty nextLink ends paging.
func resolveNext(current, nextLink string) string {
	if nextLink == "" {
		return ""
	}
	base, err := url.Parse(current)
	if err != nil {
		return nextLink
	}
	ref, err := url.Parse(nextLink)
	if err != nil {
		return nextLink
	}
	return base.ResolveReference(ref).String()
}

// serviceDoc is the relevant shape of an OData service document (root). Both v2
// and v4 JSON service docs expose an array of entity sets with a "name" field
// (v4 under "value", v2 under "d.EntitySets" or "d"); we read whichever exists.
type serviceDoc struct {
	Value []struct {
		Name string `json:"name"`
	} `json:"value"`
	D struct {
		EntitySets []string `json:"EntitySets"`
	} `json:"d"`
}

// EntitySets lists the entity-set names from the service document at serviceURL.
func EntitySets(ctx context.Context, serviceURL, token string) ([]string, error) {
	body, err := doGET(ctx, serviceURL, token)
	if err != nil {
		return nil, err
	}
	var doc serviceDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("odata: decode service document from %q: %w", serviceURL, err)
	}
	names := make([]string, 0, len(doc.Value))
	for _, e := range doc.Value {
		if e.Name != "" {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		names = append(names, doc.D.EntitySets...)
	}
	return names, nil
}

// --- $metadata (EDMX) parsing ---------------------------------------------

// PropertyRow is one property of an EntityType in the EDMX metadata document.
type PropertyRow struct {
	EntityType string
	Property   string
	Type       string
}

// edmx mirrors the subset of the CSDL/EDMX XML we care about: entity types and
// their properties. It is deliberately namespace-agnostic (xml.Name matched on
// Local) so it parses both v2 (EDM 1.0–3.0) and v4 (EDM 4.0) metadata.
type edmx struct {
	XMLName     xml.Name     `xml:"Edmx"`
	Schemas     []edmxSchema `xml:"DataServices>Schema"`
	BareSchemas []edmxSchema `xml:"Schema"`
}

type edmxSchema struct {
	Namespace   string           `xml:"Namespace,attr"`
	EntityTypes []edmxEntityType `xml:"EntityType"`
}

type edmxEntityType struct {
	Name       string         `xml:"Name,attr"`
	Properties []edmxProperty `xml:"Property"`
}

type edmxProperty struct {
	Name string `xml:"Name,attr"`
	Type string `xml:"Type,attr"`
}

// Metadata fetches and parses the $metadata (EDMX XML) document for the service
// at serviceURL, returning one row per (entity type, property).
func Metadata(ctx context.Context, serviceURL, token string) ([]PropertyRow, error) {
	metaURL := strings.TrimRight(serviceURL, "/") + "/$metadata"
	body, err := doGET(ctx, metaURL, token)
	if err != nil {
		return nil, err
	}
	var doc edmx
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("odata: parse $metadata EDMX from %q: %w", metaURL, err)
	}
	schemas := doc.Schemas
	if len(schemas) == 0 {
		schemas = doc.BareSchemas
	}
	var rows []PropertyRow
	for _, s := range schemas {
		for _, et := range s.EntityTypes {
			for _, p := range et.Properties {
				rows = append(rows, PropertyRow{
					EntityType: et.Name,
					Property:   p.Name,
					Type:       p.Type,
				})
			}
		}
	}
	return rows, nil
}
