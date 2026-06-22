// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"testing"

	"github.com/Query-farm/vgi-go/vgi"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// strCol builds a 1-row string array (optionally NULL) for use as a positional
// argument value.
func strCol(t *testing.T, v string, null bool) arrow.Array {
	t.Helper()
	b := array.NewStringBuilder(memory.DefaultAllocator)
	defer b.Release()
	if null {
		b.AppendNull()
	} else {
		b.Append(v)
	}
	return b.NewArray()
}

// argsWith builds *vgi.Arguments with the given positional arrays.
func argsWith(positional ...arrow.Array) *vgi.Arguments {
	return &vgi.Arguments{
		Positional: positional,
		Named:      map[string]arrow.Array{},
	}
}

func TestQueryNewStateData(t *testing.T) {
	ts, _ := newMock(t)
	f := &QueryFunction{}
	st, err := f.NewState(&vgi.ProcessParams{
		Args: argsWith(strCol(t, ts.URL, false), strCol(t, "People", false)),
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(st.Entities) == 0 {
		t.Fatal("expected entities from NewState")
	}
	if st.Done {
		t.Error("state should not be marked done before Process")
	}
}

func TestQueryNullServiceURLNoRows(t *testing.T) {
	f := &QueryFunction{}
	st, err := f.NewState(&vgi.ProcessParams{
		Args: argsWith(strCol(t, "", true), strCol(t, "People", false)),
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(st.Entities) != 0 {
		t.Error("NULL service_url should yield no rows")
	}
}

func TestEntitySetsNewStateData(t *testing.T) {
	ts, _ := newMock(t)
	f := &EntitySetsFunction{}
	st, err := f.NewState(&vgi.ProcessParams{
		Args: argsWith(strCol(t, ts.URL, false)),
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(st.Names) != 1 || st.Names[0] != "People" {
		t.Fatalf("expected [People], got %v", st.Names)
	}
}

func TestMetadataNewStateData(t *testing.T) {
	ts, _ := newMock(t)
	f := &MetadataFunction{}
	st, err := f.NewState(&vgi.ProcessParams{
		Args: argsWith(strCol(t, ts.URL, false)),
	})
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if len(st.Rows) != 3 {
		t.Fatalf("expected 3 property rows, got %d", len(st.Rows))
	}
}

func TestRegisterDoesNotPanic(t *testing.T) {
	// Registration runs the SDK's gob-encodability validation on each state type;
	// a non-encodable state field would panic here.
	w := vgi.NewWorker(vgi.WithCatalogName(CatalogName))
	Register(w)
}
