// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Query-farm/vgi-go/vgi"
)

// TestExamplesJSONRoundTrip checks the vgi.example_queries rendering keeps every
// description and SQL verbatim, including embedded quotes and '<'/'>' (no HTML
// escaping).
func TestExamplesJSONRoundTrip(t *testing.T) {
	in := []vgi.CatalogExample{
		{Description: "quotes \"x\" and a < b", SQL: "SELECT 1 FROM t WHERE \"select\" := 'a''b'"},
	}
	out := ExamplesJSON(in)
	if !strings.Contains(out, "a < b") {
		t.Fatalf("HTML-escaped output: %s", out)
	}
	var got []struct {
		Description string `json:"description"`
		SQL         string `json:"sql"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(got) != 1 || got[0].Description != in[0].Description || got[0].SQL != in[0].SQL {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

// TestFunctionExamplesWellFormed checks each function's example list: every
// example is described and catalog-qualified, the vgi.example_queries tag
// mirrors Examples, and every composed example sits within the first five
// examples of at least one function (agents see only the first five).
func TestFunctionExamplesWellFormed(t *testing.T) {
	type named interface {
		Name() string
		Metadata() vgi.FunctionMetadata
	}
	fns := []named{&QueryFunction{}, &EntitySetsFunction{}, &MetadataFunction{}}
	firstFive := map[string]bool{}
	for _, f := range fns {
		md := f.Metadata()
		if got, want := md.Tags["vgi.example_queries"], ExamplesJSON(md.Examples); got != want {
			t.Errorf("%s: vgi.example_queries does not mirror Examples", f.Name())
		}
		for i, e := range md.Examples {
			if strings.TrimSpace(e.Description) == "" {
				t.Errorf("%s example %d has no description", f.Name(), i)
			}
			if !strings.Contains(e.SQL, "odata.main.") {
				t.Errorf("%s example %d is not catalog-qualified: %s", f.Name(), i, e.SQL)
			}
			if i < 5 {
				firstFive[e.SQL] = true
			}
		}
	}
	for _, c := range ComposedExamples {
		if !firstFive[c.SQL] {
			t.Errorf("composed example not in any function's first five: %s", c.Description)
		}
		if n := strings.Count(c.SQL, "odata.main.odata_"); n < 2 {
			t.Errorf("composed example calls %d worker objects, want >= 2: %s", n, c.Description)
		}
	}
}
