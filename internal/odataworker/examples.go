// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"bytes"
	"encoding/json"

	"github.com/Query-farm/vgi-go/vgi"
)

// Public, auth-free reference services the example queries run against.
const (
	tripPinURL   = "https://services.odata.org/V4/TripPinService"
	northwindURL = "https://services.odata.org/V4/Northwind/Northwind.svc"
)

// Composed examples combine two or more of the worker's functions in one query.
// Each is defined once here and referenced from both the driving function's
// example list and the schema-level vgi.example_queries (main.go).
var (
	// ExamplePersonCompleteness drives odata_query's rows from odata_metadata's
	// declared properties: a data-completeness profile of an entity set.
	ExamplePersonCompleteness = vgi.CatalogExample{
		Description: "Profile data completeness: for every property the Person entity type declares in " +
			"$metadata, count how many TripPin People entities carry a non-empty value (not null, an " +
			"empty string, or an empty collection), least-populated first.",
		SQL: "SELECT m.property, m.type, " +
			"count(*) FILTER (WHERE coalesce(json_extract(p.entity, '$.' || m.property)::VARCHAR, 'null') NOT IN ('null', '\"\"', '[]')) AS populated, " +
			"count(*) AS people " +
			"FROM odata.main.odata_metadata('" + tripPinURL + "') AS m " +
			"CROSS JOIN odata.main.odata_query('" + tripPinURL + "', 'People') AS p " +
			"WHERE m.entity_type = 'Person' " +
			"GROUP BY m.property, m.type ORDER BY populated, m.property",
	}

	// ExampleProductNumericProfile uses odata_metadata's EDM types to pick the
	// numeric columns of an entity set read with odata_query, then summarises them.
	ExampleProductNumericProfile = vgi.CatalogExample{
		Description: "Summarise every numeric column of the Northwind Products entity set: take the Product " +
			"properties $metadata declares as Edm.Int16/Int32/Decimal/Double and compute each one's min, max " +
			"and average across all products.",
		SQL: "WITH product_values AS (" +
			"SELECT m.property, m.type, TRY_CAST(json_extract_string(p.entity, '$.' || m.property) AS DOUBLE) AS amount " +
			"FROM odata.main.odata_metadata('" + northwindURL + "') AS m " +
			"CROSS JOIN odata.main.odata_query('" + northwindURL + "', 'Products') AS p " +
			"WHERE m.entity_type = 'Product' AND m.type IN ('Edm.Int16', 'Edm.Int32', 'Edm.Decimal', 'Edm.Double')) " +
			"SELECT property, type, min(amount) AS min_value, max(amount) AS max_value, round(avg(amount), 2) AS avg_value " +
			"FROM product_values GROUP BY property, type ORDER BY property",
	}

	// ExampleTopCustomersByOrders joins two entity sets of one service
	// client-side, pushing $select down so only the join fields are fetched.
	ExampleTopCustomersByOrders = vgi.CatalogExample{
		Description: "Join two entity sets client-side: the five Northwind customers with the most orders, " +
			"matching Orders to Customers on CustomerID and pushing $select down so only the needed fields " +
			"are fetched.",
		SQL: "SELECT json_extract_string(c.entity, '$.CompanyName') AS company, " +
			"json_extract_string(c.entity, '$.Country') AS country, count(*) AS orders " +
			"FROM odata.main.odata_query('" + northwindURL + "', 'Customers', \"select\" := 'CustomerID,CompanyName,Country') AS c " +
			"JOIN odata.main.odata_query('" + northwindURL + "', 'Orders', \"select\" := 'OrderID,CustomerID') AS o " +
			"ON json_extract_string(o.entity, '$.CustomerID') = json_extract_string(c.entity, '$.CustomerID') " +
			"GROUP BY company, country ORDER BY orders DESC, company LIMIT 5",
	}
)

// ComposedExamples lists every composed example, for the schema-level
// vgi.example_queries.
var ComposedExamples = []vgi.CatalogExample{
	ExamplePersonCompleteness,
	ExampleProductNumericProfile,
	ExampleTopCustomersByOrders,
}

// ExamplesJSON renders examples as a vgi.example_queries tag value: a JSON list
// of {description, sql} objects (VGI502/VGI515). duckdb_functions().examples
// drops CatalogExample descriptions, so the described list travels as this tag.
func ExamplesJSON(examples []vgi.CatalogExample) string {
	type described struct {
		Description string `json:"description"`
		SQL         string `json:"sql"`
	}
	list := make([]described, len(examples))
	for i, e := range examples {
		list[i] = described{Description: e.Description, SQL: e.SQL}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(list); err != nil {
		return "[]"
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// Per-function example lists. They feed both FunctionMetadata.Examples and the
// function's vgi.example_queries tag; composed examples sit within the first
// five so agents (which see only the first five per object) find them.
var (
	queryExamples = []vgi.CatalogExample{
		{
			Description: "Read the first 5 People entities from the public TripPin OData v4 service and pull a field out of the raw JSON.",
			SQL:         "SELECT seq, json_extract_string(entity, '$.FirstName') AS first_name FROM odata.main.odata_query('" + tripPinURL + "', 'People', top := '5');",
		},
		ExampleTopCustomersByOrders,
		ExampleProductNumericProfile,
		{
			Description: "Count the German customers in the public Northwind OData v4 sample, pushing the predicate down to the service via the OData $filter option.",
			SQL:         "SELECT count(*) FROM odata.main.odata_query('" + northwindURL + "', 'Customers', \"filter\" := 'Country eq ''Germany''', max_rows := 1000);",
		},
	}

	entitySetsExamples = []vgi.CatalogExample{
		{
			Description: "Discover every entity set the TripPin OData service exposes (People, Airlines, Airports, ...).",
			SQL:         "SELECT name FROM odata.main.odata_entity_sets('" + tripPinURL + "') ORDER BY name;",
		},
	}

	metadataExamples = []vgi.CatalogExample{
		{
			Description: "Inspect the properties and EDM types of the Person entity type from the service's $metadata (EDMX) document.",
			SQL:         "SELECT property, type FROM odata.main.odata_metadata('" + tripPinURL + "') WHERE entity_type = 'Person';",
		},
		ExamplePersonCompleteness,
	}
)
