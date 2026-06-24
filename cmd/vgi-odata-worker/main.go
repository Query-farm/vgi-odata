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

func main() {
	// Accept --http for HTTP transport and --unix for the AF_UNIX launcher
	// transport; default is stdio. Unknown launcher flags are tolerated (the
	// VGI extension varies argv to key its worker cache), so we filter to flags
	// we actually define before parsing.
	httpMode := flag.Bool("http", false, "Run as an HTTP server instead of stdio")
	unixPath := flag.String("unix", "", "Serve the AF_UNIX launcher transport on this socket path instead of stdio")
	logFlags := vgi.RegisterLoggingFlags(flag.CommandLine)
	_ = flag.CommandLine.Parse(filterKnownFlags(os.Args[1:], map[string]bool{
		"log-level":  true,
		"log-format": true,
		"log-logger": true,
		"unix":       true,
	}))
	if err := logFlags.Apply(); err != nil {
		log.Fatalf("logging flags: %v", err)
	}

	sourceURL := "https://github.com/Query-farm/vgi-odata"
	w := vgi.NewWorker(
		vgi.WithCatalogName(odataworker.CatalogName),
		vgi.WithCatalogComment("Query OData v2/v4 services and return their entities as rows"),
		vgi.WithCatalogTags(map[string]string{
			"source": "vgi-odata",
			"vgi.description_llm": "Read data from OData v2/v4 REST services directly in SQL. " +
				"OData is the JSON/REST protocol behind Microsoft Dynamics 365 / Dataverse, " +
				"SAP Gateway (S/4HANA, NetWeaver), and many enterprise APIs, so this is a " +
				"generic OData reader rather than a vendor-specific connector. Use it to " +
				"discover the entity sets a service exposes (odata_entity_sets), read an " +
				"entity set as rows of raw JSON following nextLink paging with $filter/$select/" +
				"$orderby/$top pushdown (odata_query), and inspect an entity type's properties " +
				"and types from its $metadata/EDMX document (odata_metadata). Supports bearer-" +
				"token auth and both v4 (value/@odata.nextLink) and v2 (d.results/d.__next) shapes.",
			"vgi.description_md": "# odata\n\n" +
				"Query **OData v2/v4** services from DuckDB/SQL and return their entities as rows.\n\n" +
				"A generic OData reader over Apache Arrow — works against TripPin, Northwind, " +
				"Microsoft Dynamics / Dataverse, SAP Gateway, or any spec-compliant OData service.\n\n" +
				"Table functions:\n" +
				"- `odata_entity_sets(service_url)` — list the entity sets in a service document.\n" +
				"- `odata_query(service_url, entity_set, ...)` — read an entity set; one row per " +
				"entity as raw JSON, following nextLink paging.\n" +
				"- `odata_metadata(service_url)` — parse `$metadata` (EDMX) into one row per " +
				"entity-type property.",
			"vgi.author":             "Query.Farm",
			"vgi.copyright":          "Copyright 2026 Query Farm LLC - https://query.farm",
			"vgi.license":            "MIT",
			"vgi.support_contact":    "https://github.com/Query-farm/vgi-odata/issues",
			"vgi.support_policy_url": "https://github.com/Query-farm/vgi-odata/blob/main/README.md",
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
				"vgi.description_llm": "OData read functions: list a service's entity sets, read an " +
					"entity set as rows of raw JSON (with $filter/$select/$orderby/$top and nextLink " +
					"paging), and parse an entity type's properties and types from $metadata/EDMX.",
				"vgi.description_md": "OData v2/v4 read functions over Apache Arrow: " +
					"`odata_entity_sets`, `odata_query`, and `odata_metadata`.",
			},
		}),
	)
	odataworker.Register(w)

	if *httpMode {
		if err := w.RunHttp("127.0.0.1:0"); err != nil {
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
