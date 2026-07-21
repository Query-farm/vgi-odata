// Copyright 2026 Query Farm LLC - https://query.farm

// Command vgi-odata-worker is a VGI worker that queries OData v2/v4 services
// (Dynamics, SAP Gateway, and many other enterprise REST APIs) and returns
// their entities as DuckDB rows. It speaks the VGI protocol over stdio.
package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/Query-farm/vgi-go/vgi"
	"github.com/Query-farm/vgi-odata/internal/odataworker"
)

// odataExecutableExamples is the catalog's vgi.executable_examples tag (VGI509):
// a JSON list of {"description","sql"} objects whose SQL is catalog-qualified and
// self-contained. They run against Microsoft's public, auth-free TripPin OData v4
// reference service (services.odata.org).
const odataExecutableExamples = `[
  {
    "description": "Discover the entity sets the TripPin OData service exposes.",
    "sql": "SELECT name FROM odata.main.odata_entity_sets('https://services.odata.org/V4/TripPinService') ORDER BY name"
  },
  {
    "description": "Read the first 5 People entities and pull a field out of the raw JSON.",
    "sql": "SELECT seq, json_extract_string(entity, '$.FirstName') AS first_name FROM odata.main.odata_query('https://services.odata.org/V4/TripPinService', 'People', top := '5') ORDER BY seq"
  },
  {
    "description": "Inspect the properties and EDM types of the Person entity type from $metadata.",
    "sql": "SELECT property, type FROM odata.main.odata_metadata('https://services.odata.org/V4/TripPinService') WHERE entity_type = 'Person' ORDER BY property"
  }
]`

// odataAgentTestTasks is the catalog's vgi.agent_test_tasks suite (VGI152/VGI920):
// a JSON array of {name, prompt, reference_sql, ...} analyst tasks that
// `vgi-lint simulate` uses to measure how well an agent can actually use this
// worker. Only each task's `prompt` reaches the analyst; `reference_sql` is
// grader-only. Both the analyst's query and the reference run against the live,
// auth-free TripPin OData v4 service, so grading stays consistent even though
// TripPin is a mutable sandbox. Together the tasks cover all three functions.
const odataAgentTestTasks = `[
  {
    "name": "list-entity-sets",
    "prompt": "Using the OData service at https://services.odata.org/V4/TripPinService, list the names of every entity set it exposes.",
    "reference_sql": "SELECT name FROM odata.main.odata_entity_sets('https://services.odata.org/V4/TripPinService') ORDER BY name",
    "unordered": true,
    "success_criteria": "Returns the entity-set names the TripPin service advertises (e.g. People, Airlines, Airports)."
  },
  {
    "name": "people-named-russell",
    "prompt": "Using the OData service at https://services.odata.org/V4/TripPinService, determine whether any Person in the People entity set has the FirstName 'Russell'. Answer with a single boolean value.",
    "reference_sql": "SELECT count(*) > 0 AS has_russell FROM odata.main.odata_query('https://services.odata.org/V4/TripPinService', 'People', \"filter\" := 'FirstName eq ''Russell''')",
    "ignore_column_names": true,
    "success_criteria": "Returns a single boolean that is true because at least one Person named Russell exists (Russell Whyte)."
  },
  {
    "name": "count-all-people",
    "prompt": "How many People entities does the TripPin OData service at https://services.odata.org/V4/TripPinService contain in total (across all pages)?",
    "reference_sql": "SELECT count(*) AS n FROM odata.main.odata_query('https://services.odata.org/V4/TripPinService', 'People')",
    "ignore_column_names": true,
    "success_criteria": "Returns a single total count of People entities, having followed nextLink paging."
  },
  {
    "name": "person-type-properties",
    "prompt": "What properties does the 'Person' entity type expose in the OData service at https://services.odata.org/V4/TripPinService, and what is each property's EDM type?",
    "reference_sql": "SELECT property, type FROM odata.main.odata_metadata('https://services.odata.org/V4/TripPinService') WHERE entity_type = 'Person' ORDER BY property",
    "unordered": true,
    "success_criteria": "Lists the Person entity type's properties with their EDM types from $metadata."
  },
  {
    "name": "list-known-public-services",
    "prompt": "This OData extension ships a built-in registry of well-known public OData services. List the name and service_url of every OData v4 service in that registry.",
    "reference_sql": "SELECT name, service_url FROM odata.main.odata_public_services WHERE odata_version = 'v4' ORDER BY name",
    "unordered": true,
    "success_criteria": "Lists the registry's OData v4 services with their root URLs (e.g. TripPin, Northwind V4)."
  }
]`

func main() {
	// Accept --http for HTTP transport and --unix for the AF_UNIX launcher
	// transport; default is stdio. Unknown launcher flags are tolerated (the
	// VGI extension varies argv to key its worker cache), so we filter to flags
	// we actually define before parsing.
	httpMode := flag.Bool("http", false, "Run as an HTTP server instead of stdio")
	// httpAddr is the bind address for --http. It defaults to 127.0.0.1:0 (an
	// ephemeral loopback port, unchanged for dev/CI where the SDK prints the
	// chosen port as "PORT:<n>"); the container entrypoint overrides it with
	// 0.0.0.0:$PORT so a published host port and the /health probe can reach it.
	httpAddr := flag.String("http-addr", "127.0.0.1:0", "HTTP listen address when --http is set (host:port; port 0 = pick a free ephemeral port)")
	unixPath := flag.String("unix", "", "Serve the AF_UNIX launcher transport on this socket path instead of stdio")
	logFlags := vgi.RegisterLoggingFlags(flag.CommandLine)
	_ = flag.CommandLine.Parse(filterKnownFlags(os.Args[1:], map[string]bool{
		"log-level":  true,
		"log-format": true,
		"log-logger": true,
		"unix":       true,
		"http-addr":  true,
	}))
	if err := logFlags.Apply(); err != nil {
		log.Fatalf("logging flags: %v", err)
	}

	sourceURL := "https://github.com/Query-farm/vgi-odata"
	w := vgi.NewWorker(
		vgi.WithCatalogName(odataworker.CatalogName),
		vgi.WithCatalogComment("Query OData v2/v4 services and return their entities as rows"),
		vgi.WithCatalogTags(map[string]string{
			"source":    "vgi-odata",
			"vgi.title": "OData v2/v4 Reader",
			// VGI138: vgi.keywords must be a JSON array of strings.
			"vgi.keywords": `["odata","odata reader","rest","json","http","entity set",` +
				`"$metadata","edmx","nextlink paging","$filter","$select","$orderby","$top",` +
				`"dynamics 365","dataverse","sap gateway","s/4hana","netweaver",` +
				`"enterprise api","bearer token"]`,
			"vgi.doc_llm": "Read data from OData v2/v4 REST services directly in SQL. " +
				"OData is the JSON/REST protocol behind Microsoft Dynamics 365 / Dataverse, " +
				"SAP Gateway (S/4HANA, NetWeaver), and many enterprise APIs, so this is a " +
				"generic OData reader rather than a vendor-specific connector. Use it to " +
				"discover the entity sets a service exposes, read an " +
				"entity set as rows of raw JSON following nextLink paging with " +
				"`$filter`/`$select`/`$orderby`/`$top` pushdown, and inspect an entity type's " +
				"properties and EDM types from its `$metadata`/EDMX document. Supports bearer-" +
				"token auth and both v4 (value/@odata.nextLink) and v2 (d.results/d.__next) shapes.",
			"vgi.doc_md": "# OData v2/v4 Reader for DuckDB\n\n" +
				"**Query any OData v2 or v4 REST service directly in SQL** — discover its entity " +
				"sets, read entities as rows, and inspect `$metadata`, all without writing a line " +
				"of HTTP or JSON-parsing code.\n\n" +
				"OData (the Open Data Protocol) is the JSON-over-REST standard behind a huge slice " +
				"of enterprise data: Microsoft Dynamics 365 and Dataverse, SAP Gateway " +
				"(S/4HANA and NetWeaver), Microsoft Graph, and countless in-house APIs all speak " +
				"it. This extension turns those services into queryable tables, so analysts and " +
				"data engineers can join live OData entities against the rest of their warehouse " +
				"using plain SQL. It is a *generic* OData reader, not a vendor-specific connector, " +
				"so the same three functions work against TripPin, Northwind, Dynamics, SAP, or " +
				"any spec-compliant service.\n\n" +
				"Under the hood the worker speaks OData over HTTP using only the Go standard " +
				"library — no heavyweight SDK — and streams results to DuckDB over Apache Arrow. " +
				"It transparently follows server-driven paging (the v4 `@odata.nextLink` and the " +
				"v2 `d.__next` cursor), pushes `$filter`, `$select`, `$orderby`, and `$top` down " +
				"to the service so the server does the work, and understands both response shapes: " +
				"v4 (`value` / `@odata.nextLink`) and v2 (`d.results` / `d.__next`). Protected " +
				"services are supported via an optional bearer token argument.\n\n" +
				"The `main` schema exposes a small set of read functions covering the whole " +
				"workflow: discovering the entity sets a service advertises in its service " +
				"document, reading an entity set as rows of raw JSON (with `$filter`/`$select`/" +
				"`$orderby`/`$top` pushdown, automatic nextLink paging, and a configurable row cap), " +
				"and parsing an entity type's properties and EDM types from its `$metadata` (EDMX) " +
				"document — namespace-agnostically across both EDM 3.0 (v2) and EDM 4.0 (v4). It " +
				"also ships a browsable registry of well-known auth-free public services so you can " +
				"start querying without a URL in hand. The catalog's executable examples and " +
				"per-object example queries provide ready-to-run SQL, and the usual path is " +
				"discover → inspect → read.\n\n" +
				"## Learn more\n\n" +
				"- [OData protocol homepage](https://www.odata.org)\n" +
				"- [OData official documentation](https://www.odata.org/documentation/)\n" +
				"- [Microsoft OData documentation](https://learn.microsoft.com/en-us/odata/)\n" +
				"- [vgi-odata source code](https://github.com/Query-farm/vgi-odata)\n" +
				"- [vgi-go SDK](https://github.com/Query-farm/vgi-go)",
			"vgi.author":             "Query.Farm",
			"vgi.copyright":          "Copyright 2026 Query Farm LLC - https://query.farm",
			"vgi.license":            "MIT",
			"vgi.support_contact":    "https://github.com/Query-farm/vgi-odata/issues",
			"vgi.support_policy_url": "https://github.com/Query-farm/vgi-odata/blob/main/README.md",
			// VGI509: at least one guaranteed-runnable, catalog-qualified example.
			// These run against Microsoft's public TripPin OData v4 reference
			// service (services.odata.org), which has no auth and is the canonical
			// OData test service.
			"vgi.executable_examples": odataExecutableExamples,
			// VGI152/VGI920: agent-suitability task suite for `vgi-lint simulate`.
			"vgi.agent_test_tasks": odataAgentTestTasks,
		}),
		vgi.WithCatalogInfo(vgi.CatalogInfo{
			Name:      odataworker.CatalogName,
			SourceURL: &sourceURL,
		}),
		vgi.WithSchemaComments(map[string]string{
			"main": "OData read functions: entity-set discovery, entity reads, and $metadata parsing.",
		}),
		vgi.WithSchemaTags(map[string]map[string]string{
			"main": {
				"vgi.title": "OData Read Functions",
				// VGI413: ordered category registry; each function names one via
				// its vgi.category tag. Categories drive navigation/listing/SEO.
				"vgi.categories": `[` +
					`{"name":"Discovery","description":"Explore what an OData service offers — ` +
					`list its entity sets and inspect entity-type schemas from $metadata."},` +
					`{"name":"Query","description":"Read entity data from an OData service as ` +
					`rows of raw JSON, with $filter/$select/$orderby/$top pushdown and paging."}` +
					`]`,
				// VGI138: vgi.keywords must be a JSON array of strings.
				"vgi.keywords": `["odata","entity sets","odata_query","odata_entity_sets",` +
					`"odata_metadata","rest","json","http","paging","nextlink","$filter",` +
					`"$select","$orderby","$top","edmx","discovery","dynamics","dataverse",` +
					`"sap gateway"]`,
				// VGI139: vgi.source_url belongs only on the catalog, not per-object.
				// VGI123 classifying tags use BARE keys (not vgi.-namespaced).
				"domain":   "enterprise-data",
				"category": "data-integration",
				"topic":    "odata-rest-api",
				// VGI506/VGI515 representative example queries for the schema: a JSON
				// list of {description, sql} objects (catalog-qualified SQL), so every
				// example carries a human-readable description.
				"vgi.example_queries": `[` +
					`{"description":"Discover every entity set the TripPin OData service exposes.",` +
					`"sql":"SELECT name FROM odata.main.odata_entity_sets('https://services.odata.org/V4/TripPinService') ORDER BY name"},` +
					`{"description":"Read the first 5 People entities and project a field out of the raw JSON.",` +
					`"sql":"SELECT seq, json_extract_string(entity, '$.FirstName') AS first_name FROM odata.main.odata_query('https://services.odata.org/V4/TripPinService', 'People', top := '5')"},` +
					`{"description":"Inspect the Person entity type's properties and EDM types from $metadata.",` +
					`"sql":"SELECT property, type FROM odata.main.odata_metadata('https://services.odata.org/V4/TripPinService') WHERE entity_type = 'Person'"}` +
					`]`,
				"vgi.doc_llm": "The `main` schema groups the three OData read functions. A typical " +
					"workflow is: call `odata_entity_sets(service_url)` to discover what an OData service " +
					"exposes, optionally `odata_metadata(service_url)` to learn an entity type's " +
					"properties and EDM types, then `odata_query(service_url, entity_set, ...)` to read " +
					"the data as rows of raw JSON with `$filter`/`$select`/`$orderby`/`$top` pushdown and " +
					"automatic nextLink paging. All three accept an optional bearer token and work " +
					"against both OData v4 (value/@odata.nextLink) and v2 (d.results/d.__next) services.",
				"vgi.doc_md": "## OData read functions\n\n" +
					"Functions for reading **OData v2/v4** services from SQL over Apache Arrow.\n\n" +
					"### Functions\n\n" +
					"- **`odata_entity_sets(service_url)`** — list the entity sets a service advertises " +
					"in its service document.\n" +
					"- **`odata_query(service_url, entity_set, ...)`** — read an entity set; one row per " +
					"entity as raw JSON, following nextLink paging with `$filter`/`$select`/`$orderby`/" +
					"`$top` pushdown.\n" +
					"- **`odata_metadata(service_url)`** — parse `$metadata` (EDMX) into one row per " +
					"entity-type property.\n\n" +
					"### Notes\n\n" +
					"Start with `odata_entity_sets` to discover a service, then `odata_query` to read " +
					"rows; project JSON fields with `json_extract_string`. All functions accept an " +
					"optional bearer token for protected services.",
			},
		}),
	)
	odataworker.Register(w)

	if *httpMode {
		if err := w.RunHttp(*httpAddr); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *unixPath != "" {
		// AF_UNIX launcher transport: serve on the given socket path. The SDK
		// prints "UNIX:<path>" once listening; idleTimeout=0 disables the
		// self-shutdown timer (the launcher/CI owns the process lifecycle).
		if err := w.RunUnix(*unixPath, 0); err != nil {
			log.Fatal(err)
		}
		return
	}
	w.RunStdio()
}

// filterKnownFlags drops argv tokens for flags this binary doesn't define, so
// launcher-injected differentiation flags don't abort flag parsing. Flags named
// in valueFlags consume the following token as their value.
func filterKnownFlags(args []string, valueFlags map[string]bool) []string {
	defined := map[string]bool{}
	flag.CommandLine.VisitAll(func(f *flag.Flag) { defined[f.Name] = true })
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		hasInlineValue := strings.ContainsRune(name, '=')
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if !defined[name] {
			continue
		}
		out = append(out, a)
		if valueFlags[name] && !hasInlineValue && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out
}
