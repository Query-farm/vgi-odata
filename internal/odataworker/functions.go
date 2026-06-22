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
// Process. The Done field is exported so gob can round-trip it.
type emitState struct {
	Done bool
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

// queryState holds the fetched entities (gob-encodable) plus the emit flag.
type queryState struct {
	emitState
	Entities []Entity
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
	if state.Done {
		return out.Finish()
	}
	state.Done = true
	e := state.Entities
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

// entitySetsState holds the discovered entity-set names plus the emit flag.
type entitySetsState struct {
	emitState
	Names []string
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
	if state.Done {
		return out.Finish()
	}
	state.Done = true
	n := int64(len(state.Names))
	col := vgi.BuildStringArray(n, func(i int64) string { return state.Names[i] })
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

// metadataState holds the parsed property rows plus the emit flag.
type metadataState struct {
	emitState
	Rows []PropertyRow
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
	if state.Done {
		return out.Finish()
	}
	state.Done = true
	r := state.Rows
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
