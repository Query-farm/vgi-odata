// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import "github.com/Query-farm/vgi-go/vgi"

// A worker that exposes only table functions forces an agent to guess argument
// values before it can see any data (VGI146). To give it a zero-argument,
// credential-free starting point, we ship a small curated registry of well-known
// public, auth-free OData services as a browsable view. Every row is a real
// service root URL that can be fed straight into odata_entity_sets / odata_query
// / odata_metadata, so discovery ("what can I even point this at?") no longer
// requires prior knowledge of a service URL.
//
// The view is backed by an inline VALUES list, so it scans instantly with no
// network access — it always responds to VGI911's LIMIT 10 probe even when the
// upstream services are unreachable.

// publicServicesViewDefinition is the SQL backing odata_public_services.
// Apostrophes inside the description literals are SQL-escaped (”).
const publicServicesViewDefinition = `SELECT * FROM (VALUES
  ('TripPin', 'https://services.odata.org/V4/TripPinService', 'v4', 'Microsoft''s canonical OData v4 reference service (People, Airlines, Airports, trips).'),
  ('TripPin RESTier', 'https://services.odata.org/TripPinRESTierService', 'v4', 'Read/write OData v4 TripPin variant hosted by the OData.org reference services.'),
  ('OData v4 Sample', 'https://services.odata.org/V4/OData/OData.svc', 'v4', 'The OData.org OData v4 sample service (Products, Categories, Suppliers).'),
  ('Northwind V4', 'https://services.odata.org/V4/Northwind/Northwind.svc', 'v4', 'The classic Northwind sales sample (Customers, Orders, Products) as OData v4.'),
  ('Northwind V3', 'https://services.odata.org/V3/Northwind/Northwind.svc', 'v3', 'The Northwind sales sample exposed as OData v3.'),
  ('Northwind V2', 'https://services.odata.org/V2/Northwind/Northwind.svc', 'v2', 'The Northwind sales sample as OData v2 (d.results/d.__next response shape).')
) AS t(name, service_url, odata_version, description)`

// publicServicesDocMD is the view's human-facing documentation. It intentionally
// contains no runnable SQL fence (VGI179) — the object-level example queries hold
// the ready-to-run SQL.
const publicServicesDocMD = "## odata_public_services\n\n" +
	"A curated registry of **well-known, public, authentication-free OData services** you can " +
	"query immediately — no credentials, no setup.\n\n" +
	"### Overview\n\n" +
	"Every row is a real OData service root plus its protocol version and a short description. " +
	"Use it as the zero-argument starting point for exploration: pick a `service_url`, then pass " +
	"it to `odata_entity_sets` to see what the service exposes, `odata_metadata` to inspect an " +
	"entity type's schema, or `odata_query` to read rows. Because the view is a static in-worker " +
	"registry, it scans instantly and works offline even when a listed service is unreachable.\n\n" +
	"### Columns\n\n" +
	"- `name` (`VARCHAR`) — short human-friendly service name.\n" +
	"- `service_url` (`VARCHAR`) — the service root URL; feed it to the OData functions.\n" +
	"- `odata_version` (`VARCHAR`) — `v2`, `v3`, or `v4`; pass `version := 'v2'` to `odata_query` " +
	"for a v2 service.\n" +
	"- `description` (`VARCHAR`) — what the service contains.\n\n" +
	"### Notes\n\n" +
	"The registry is a convenience sample of stable public reference services, not an exhaustive " +
	"directory — this worker can query any spec-compliant OData v2/v4 endpoint, not only these."

// publicServicesDocLLM is the agent-facing narrative.
const publicServicesDocLLM = "Browsable, zero-argument registry of well-known public, auth-free OData " +
	"services. Each row gives a service `name`, a `service_url` root you can pass straight to " +
	"odata_entity_sets / odata_metadata / odata_query, the `odata_version` (v2/v3/v4; use " +
	"version := 'v2' on odata_query for v2 services), and a `description`. Query this first when " +
	"you have no service URL in hand — it lets an agent discover something concrete to point the " +
	"OData functions at without prior knowledge. The data is a static in-worker VALUES registry, " +
	"so it scans instantly and offline."

// publicServicesExampleQueries is the view's object-level vgi.example_queries
// (VGI502: JSON array of {description, sql}; catalog-qualified; projects columns
// rather than SELECT * so it teaches real usage, VGI514).
const publicServicesExampleQueries = `[
  {
    "description": "List the auth-free public OData v4 services this worker ships, newest-style first.",
    "sql": "SELECT name, service_url FROM odata.main.odata_public_services WHERE odata_version = 'v4' ORDER BY name"
  },
  {
    "description": "Count the registered public services grouped by OData protocol version.",
    "sql": "SELECT odata_version, count(*) AS services FROM odata.main.odata_public_services GROUP BY odata_version ORDER BY odata_version"
  }
]`

// RegisterViews registers the worker's browsable catalog views.
func RegisterViews(w *vgi.Worker) {
	w.RegisterCatalogView("main", vgi.CatalogView{
		Name:       "odata_public_services",
		Definition: publicServicesViewDefinition,
		Comment:    "Curated registry of well-known public, auth-free OData services to try with the OData functions",
		ColumnComments: map[string]string{
			"name":          "Short human-friendly name of the public OData service.",
			"service_url":   "Service root URL; pass it to odata_entity_sets, odata_metadata, or odata_query.",
			"odata_version": "OData protocol version: 'v2', 'v3', or 'v4' (use version := 'v2' on odata_query for v2).",
			"description":   "What the service contains.",
		},
		Tags: map[string]string{
			"vgi.title":    "Public OData Services Registry",
			"vgi.doc_llm":  publicServicesDocLLM,
			"vgi.doc_md":   publicServicesDocMD,
			"vgi.keywords": `["odata","public services","registry","sample services","trippin","northwind","service url","discovery","auth-free","catalog","directory","odata v4","odata v2"]`,
			// VGI413: classify under the schema's "Discovery" category.
			"vgi.category": "Discovery",
			// VGI123: bare classifying tags for faceting (reused from the schema's
			// vocabulary so VGI671 sees a shared value, not a unique one).
			"domain": "enterprise-data",
			"topic":  "odata-rest-api",
			// VGI511/VGI502: object-level example queries that call this view.
			"vgi.example_queries": publicServicesExampleQueries,
		},
	})
}
