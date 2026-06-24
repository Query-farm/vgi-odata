// Copyright 2026 Query Farm LLC - https://query.farm

package odataworker

import (
	"context"

	"github.com/Query-farm/vgi-go/vgi"
	"github.com/Query-farm/vgi-rpc-go/vgirpc"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
)

// IMPORTANT: table-function state is gob-encoded by the SDK between NewState and
// Process (it may cross a process/worker boundary), so state structs must hold
// EXPORTED, gob-encodable fields only — no arrow.Record, no interfaces/chans/
// funcs, no unexported fields. Each function fetches its rows eagerly in
// NewState, stores them as plain Go slices, and rebuilds the Arrow batch in
// Process.
//
// WHY AN EXPLICIT CURSOR, NOT A bool Done (the HTTP-continuation fix):
//
// Over the HTTP transport the worker is STATELESS across exchanges — there is no
// long-lived process holding the live state between Process ticks. Instead the
// framework round-trips the producer state through an opaque continuation token:
// after each tick it gob-encodes the state (snapshotting the LIVE user state),
// the client returns the token, and the worker resumes by gob-decoding it. The
// HTTP server emits at most one data batch per response, so a producer that has
// more to emit is always resumed mid-stream from its token.
//
// The position MUST therefore live in the serialized state. A bare `Done bool`
// flipped only AFTER the single Emit does not survive the continuation boundary:
// the resumed tick observes the pre-Emit snapshot, re-emits the same rows, and
// the scan never terminates (an infinite loop — subprocess/unix keep live state
// in memory, so they were unaffected and hid the bug). Carrying an explicit
// Offset that Process advances BEFORE yielding makes the snapshot authoritative.
//
// rowsPerTick bounds how many rows each Process tick emits, so the cursor is
// observable across the continuation boundary (and scales to large results).
const rowsPerTick = 256

// cursorSlice returns the next bounded slice of rows starting at *offset and
// advances *offset past them, reporting done=true once all rows are consumed.
func cursorSlice[T any](rows []T, offset *int) (slice []T, done bool) {
	if *offset >= len(rows) {
		return nil, true
	}
	end := *offset + rowsPerTick
	if end > len(rows) {
		end = len(rows)
	}
	slice = rows[*offset:end]
	*offset = end
	return slice, false
}

// optsFrom assembles QueryOptions from the bound arguments.
func optsFrom(a queryArgs) QueryOptions {
	return QueryOptions{
		Filter:  a.Filter,
		Select:  a.Select,
		OrderBy: a.OrderBy,
		Top:     a.Top,
		MaxRows: a.MaxRows,
		Token:   a.Token,
		Version: a.Version,
	}
}

// ---------------------------------------------------------------------------
// odata_query(service_url, entity_set) -> (seq BIGINT, entity VARCHAR)
// ---------------------------------------------------------------------------

var querySchema = arrow.NewSchema([]arrow.Field{
	{Name: "seq", Type: arrow.PrimitiveTypes.Int64},
	{Name: "entity", Type: arrow.BinaryTypes.String},
}, nil)

type queryArgs struct {
	ServiceURL string `vgi:"pos=0,name=service_url,doc=OData service root URL"`
	EntitySet  string `vgi:"pos=1,name=entity_set,doc=Entity set name (path segment under the service root)"`
	Filter     string `vgi:"name=filter,default=,doc=$filter system query option"`
	Select     string `vgi:"name=select,default=,doc=$select system query option"`
	OrderBy    string `vgi:"name=orderby,default=,doc=$orderby system query option"`
	Top        string `vgi:"name=top,default=,doc=$top system query option"`
	MaxRows    int64  `vgi:"name=max_rows,default=10000,doc=Maximum entities to collect across pages"`
	Token      string `vgi:"name=token,default=,doc=Bearer token (Authorization: Bearer <token>)"`
	Version    string `vgi:"name=version,default=v4,doc=OData response shape: 'v4' (value/@odata.nextLink) or 'v2' (d.results/d.__next)"`
}

// queryState holds the fetched entities (gob-encodable) plus the cursor offset.
type queryState struct {
	Entities []Entity
	Offset   int
}

// QueryFunction reads an OData entity set as rows of raw JSON.
type QueryFunction struct{}

var _ vgi.TypedTableFunc[queryState] = (*QueryFunction)(nil)

func (f *QueryFunction) Name() string { return "odata_query" }

func (f *QueryFunction) Metadata() vgi.FunctionMetadata {
	return vgi.FunctionMetadata{
		Description: "Read an OData entity set; one row per entity (raw JSON), following nextLink paging",
		Stability:   vgi.StabilityVolatile,
		Categories:  []string{"odata", "http"},
		Tags: map[string]string{
			"vgi.columns_md": "| column | type | description |\n" +
				"|---|---|---|\n" +
				"| `seq` | BIGINT | 0-based position of the entity across all fetched pages. |\n" +
				"| `entity` | VARCHAR | The entity as its raw JSON object text; project fields with `json_extract` / `json_extract_string`. |",
		},
		Examples: []vgi.CatalogExample{
			{
				SQL: "SELECT seq, json_extract_string(entity, '$.FirstName') AS first_name " +
					"FROM odata.main.odata_query('https://services.odata.org/V4/TripPinService', 'People', top := '5');",
				Description: "Read the first 5 People entities from the public TripPin OData v4 service and pull a field out of the raw JSON.",
			},
			{
				SQL: "SELECT count(*) FROM odata.main.odata_query(" +
					"'https://services.odata.org/V4/TripPinService', 'People', " +
					"\"filter\" := 'FirstName eq ''Russell''', max_rows := 1000);",
				Description: "Count People named Russell, pushing the predicate down to the service via the OData $filter option.",
			},
		},
	}
}

func (f *QueryFunction) ArgumentSpecs() []vgi.ArgSpec { return vgi.DeriveArgSpecs(queryArgs{}) }

func (f *QueryFunction) OnBind(_ *vgi.BindParams) (*vgi.BindResponse, error) {
	return vgi.BindSchema(querySchema)
}

func (f *QueryFunction) NewState(params *vgi.ProcessParams) (*queryState, error) {
	var args queryArgs
	if err := vgi.BindArgs(params.Args, &args); err != nil {
		return nil, err
	}
	// NULL/absent service_url or entity_set → no rows.
	if isNullArg(params.Args, 0) || isNullArg(params.Args, 1) {
		return &queryState{}, nil
	}
	entities, err := Query(context.Background(), args.ServiceURL, args.EntitySet, optsFrom(args))
	if err != nil {
		return nil, err
	}
	return &queryState{Entities: entities}, nil
}

func (f *QueryFunction) Process(_ context.Context, _ *vgi.ProcessParams, state *queryState, out *vgirpc.OutputCollector) error {
	e, done := cursorSlice(state.Entities, &state.Offset)
	if done {
		return out.Finish()
	}
	n := int64(len(e))
	batch := array.NewRecordBatch(querySchema, []arrow.Array{
		vgi.BuildInt64Array(n, func(i int64) int64 { return e[i].Seq }),
		vgi.BuildStringArray(n, func(i int64) string { return e[i].Entity }),
	}, n)
	defer batch.Release()
	return out.Emit(batch)
}

// NewQueryFunction builds the registerable table function.
func NewQueryFunction() vgi.TableFunction {
	return vgi.AsTableFunction[queryState](&QueryFunction{})
}

// ---------------------------------------------------------------------------
// odata_entity_sets(service_url) -> (name VARCHAR)
// ---------------------------------------------------------------------------

var entitySetsSchema = arrow.NewSchema([]arrow.Field{
	{Name: "name", Type: arrow.BinaryTypes.String},
}, nil)

type entitySetsArgs struct {
	ServiceURL string `vgi:"pos=0,name=service_url,doc=OData service root URL"`
	Token      string `vgi:"name=token,default=,doc=Bearer token (Authorization: Bearer <token>)"`
}

// entitySetsState holds the discovered entity-set names plus the cursor offset.
type entitySetsState struct {
	Names  []string
	Offset int
}

// EntitySetsFunction lists the entity sets of a service from its service doc.
type EntitySetsFunction struct{}

var _ vgi.TypedTableFunc[entitySetsState] = (*EntitySetsFunction)(nil)

func (f *EntitySetsFunction) Name() string { return "odata_entity_sets" }

func (f *EntitySetsFunction) Metadata() vgi.FunctionMetadata {
	return vgi.FunctionMetadata{
		Description: "List the entity sets exposed by an OData service (from its service document)",
		Stability:   vgi.StabilityVolatile,
		Categories:  []string{"odata", "discovery"},
		Tags: map[string]string{
			"vgi.columns_md": "| column | type | description |\n" +
				"|---|---|---|\n" +
				"| `name` | VARCHAR | Name of an entity set advertised in the service document; pass it as the `entity_set` argument to `odata_query`. |",
		},
		Examples: []vgi.CatalogExample{
			{
				SQL:         "SELECT name FROM odata.main.odata_entity_sets('https://services.odata.org/V4/TripPinService') ORDER BY name;",
				Description: "Discover every entity set the TripPin OData service exposes (People, Airlines, Airports, ...).",
			},
		},
	}
}

func (f *EntitySetsFunction) ArgumentSpecs() []vgi.ArgSpec {
	return vgi.DeriveArgSpecs(entitySetsArgs{})
}

func (f *EntitySetsFunction) OnBind(_ *vgi.BindParams) (*vgi.BindResponse, error) {
	return vgi.BindSchema(entitySetsSchema)
}

func (f *EntitySetsFunction) NewState(params *vgi.ProcessParams) (*entitySetsState, error) {
	var args entitySetsArgs
	if err := vgi.BindArgs(params.Args, &args); err != nil {
		return nil, err
	}
	if isNullArg(params.Args, 0) {
		return &entitySetsState{}, nil
	}
	names, err := EntitySets(context.Background(), args.ServiceURL, args.Token)
	if err != nil {
		return nil, err
	}
	return &entitySetsState{Names: names}, nil
}

func (f *EntitySetsFunction) Process(_ context.Context, _ *vgi.ProcessParams, state *entitySetsState, out *vgirpc.OutputCollector) error {
	names, done := cursorSlice(state.Names, &state.Offset)
	if done {
		return out.Finish()
	}
	n := int64(len(names))
	col := vgi.BuildStringArray(n, func(i int64) string { return names[i] })
	batch := array.NewRecordBatch(entitySetsSchema, []arrow.Array{col}, n)
	defer batch.Release()
	return out.Emit(batch)
}

// NewEntitySetsFunction builds the registerable table function.
func NewEntitySetsFunction() vgi.TableFunction {
	return vgi.AsTableFunction[entitySetsState](&EntitySetsFunction{})
}

// ---------------------------------------------------------------------------
// odata_metadata(service_url) -> (entity_type VARCHAR, property VARCHAR, type VARCHAR)
// ---------------------------------------------------------------------------

var metadataSchema = arrow.NewSchema([]arrow.Field{
	{Name: "entity_type", Type: arrow.BinaryTypes.String},
	{Name: "property", Type: arrow.BinaryTypes.String},
	{Name: "type", Type: arrow.BinaryTypes.String},
}, nil)

type metadataArgs struct {
	ServiceURL string `vgi:"pos=0,name=service_url,doc=OData service root URL"`
	Token      string `vgi:"name=token,default=,doc=Bearer token (Authorization: Bearer <token>)"`
}

// metadataState holds the parsed property rows plus the cursor offset.
type metadataState struct {
	Rows   []PropertyRow
	Offset int
}

// MetadataFunction parses $metadata (EDMX) into property rows.
type MetadataFunction struct{}

var _ vgi.TypedTableFunc[metadataState] = (*MetadataFunction)(nil)

func (f *MetadataFunction) Name() string { return "odata_metadata" }

func (f *MetadataFunction) Metadata() vgi.FunctionMetadata {
	return vgi.FunctionMetadata{
		Description: "Parse an OData service's $metadata (EDMX XML); one row per entity-type property",
		Stability:   vgi.StabilityVolatile,
		Categories:  []string{"odata", "discovery"},
		Tags: map[string]string{
			"vgi.columns_md": "| column | type | description |\n" +
				"|---|---|---|\n" +
				"| `entity_type` | VARCHAR | Name of the EDM entity type the property belongs to. |\n" +
				"| `property` | VARCHAR | Name of a property declared on that entity type. |\n" +
				"| `type` | VARCHAR | The property's EDM type, e.g. `Edm.String`, `Edm.Int32`, `Edm.DateTimeOffset`. |",
		},
		Examples: []vgi.CatalogExample{
			{
				SQL: "SELECT property, type FROM odata.main.odata_metadata(" +
					"'https://services.odata.org/V4/TripPinService') WHERE entity_type = 'Person';",
				Description: "Inspect the properties and EDM types of the Person entity type from the service's $metadata (EDMX) document.",
			},
		},
	}
}

func (f *MetadataFunction) ArgumentSpecs() []vgi.ArgSpec {
	return vgi.DeriveArgSpecs(metadataArgs{})
}

func (f *MetadataFunction) OnBind(_ *vgi.BindParams) (*vgi.BindResponse, error) {
	return vgi.BindSchema(metadataSchema)
}

func (f *MetadataFunction) NewState(params *vgi.ProcessParams) (*metadataState, error) {
	var args metadataArgs
	if err := vgi.BindArgs(params.Args, &args); err != nil {
		return nil, err
	}
	if isNullArg(params.Args, 0) {
		return &metadataState{}, nil
	}
	rows, err := Metadata(context.Background(), args.ServiceURL, args.Token)
	if err != nil {
		return nil, err
	}
	return &metadataState{Rows: rows}, nil
}

func (f *MetadataFunction) Process(_ context.Context, _ *vgi.ProcessParams, state *metadataState, out *vgirpc.OutputCollector) error {
	r, done := cursorSlice(state.Rows, &state.Offset)
	if done {
		return out.Finish()
	}
	n := int64(len(r))
	batch := array.NewRecordBatch(metadataSchema, []arrow.Array{
		vgi.BuildStringArray(n, func(i int64) string { return r[i].EntityType }),
		vgi.BuildStringArray(n, func(i int64) string { return r[i].Property }),
		vgi.BuildStringArray(n, func(i int64) string { return r[i].Type }),
	}, n)
	defer batch.Release()
	return out.Emit(batch)
}

// NewMetadataFunction builds the registerable table function.
func NewMetadataFunction() vgi.TableFunction {
	return vgi.AsTableFunction[metadataState](&MetadataFunction{})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// isNullArg reports whether positional argument pos is present and NULL.
func isNullArg(args *vgi.Arguments, pos int) bool {
	if args == nil {
		return true
	}
	col, err := args.GetColumn(pos)
	if err != nil {
		return false
	}
	return col.Len() == 0 || col.IsNull(0)
}

// Register registers all OData table functions on the worker.
func Register(w *vgi.Worker) {
	w.RegisterTable(NewQueryFunction())
	w.RegisterTable(NewEntitySetsFunction())
	w.RegisterTable(NewMetadataFunction())
}
