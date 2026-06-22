# CLAUDE.md — vgi-odata

Contributor/agent notes. User-facing docs live in `README.md`; this is the
"how it's built and where the sharp edges are" companion. It is grounded in the
`vgi-grpc` Go worker template — same SDK conventions, Makefile/CI tooling, and
mock-server E2E pattern.

## What this is

A [VGI](https://query.farm) worker (Go) that queries **OData v2/v4** services
(Dynamics, SAP Gateway, and other enterprise REST APIs) and returns their
entities as DuckDB rows. Built on the [`vgi-go`](https://github.com/Query-farm/vgi-go)
SDK over stdio. Catalog name: `odata`. OData is plain REST/JSON (v4) or
REST/JSON (v2), so the worker uses only the Go stdlib (`net/http`,
`encoding/json`, `encoding/xml`, `net/url`) — **no OData SDK**.

## Layout

```
cmd/vgi-odata-worker/main.go   stdio entry point; assembles the worker + catalog
cmd/mockserver/main.go         standalone mock OData server for the SQL E2E
internal/odataworker/
  client.go                    HTTP/OData logic: Query (paging), EntitySets, Metadata; QueryOptions
  functions.go                 the three VGI table functions + Register(w)
  client_test.go               httptest unit tests (paging, options, v2 shape, errors)
  functions_test.go            VGI NewState data-path + NULL-handling tests
internal/odatamock/
  server.go                    shared mock OData v4 service (service doc, /People 2 pages, /$metadata)
test/sql/*.test                haybarn-unittest sqllogictest — authoritative E2E
Makefile                       build / test-unit / test-sql / lint
```

To add a function: implement the HTTP logic in `client.go`, wrap it as a
`vgi.TypedTableFunc` in `functions.go`, and register it in `Register(w)`.

## The Go SDK worker pattern (grounded in vgi-grpc)

`main()` assembles a `*vgi.Worker` and registers functions:

```go
w := vgi.NewWorker(vgi.WithCatalogName("odata"), vgi.WithCatalogComment("..."))
odataworker.Register(w)   // w.RegisterTable(NewXxxFunction()) for each fn
w.RunStdio()              // or w.RunHttp("127.0.0.1:0") behind a --http flag
```

A **table function** is a `vgi.TypedTableFunc[S]` (generic over a *state* type)
wrapped with `vgi.AsTableFunction[S](impl)`. Methods: `Name`, `Metadata`,
`ArgumentSpecs` (`vgi.DeriveArgSpecs(argsStruct{})`), `OnBind`
(`vgi.BindSchema(schema)`), `NewState` (bind with `vgi.BindArgs`, do the work),
`Process` (build arrays, `out.Emit(batch)`, then `out.Finish()`).

**Argument struct tags** (`vgi.DeriveArgSpecs` / `vgi.BindArgs`):

- `pos=N` → positional argument.
- A field **without** `pos` but **with** `default=` → a **named optional**
  argument (DuckDB `name := value`). `name=` sets the wire name.
- Go type → Arrow type is inferred (`string`→varchar, `int64`→bigint, …).

Build arrays in `Process` with `vgi.BuildStringArray` / `vgi.BuildInt64Array`,
then `array.NewRecordBatch(schema, []arrow.Array{...}, n)`.

## Sharp edges (learned the hard way)

1. **Table-function state is `gob`-encoded by the SDK** between `NewState` and
   `Process` (it may cross a process/worker boundary), and the SDK **panics at
   registration** if the state type isn't gob-encodable. So `S` must hold
   **exported, plain Go fields only** — no `arrow.Record`, no interfaces, chans,
   funcs, or unexported fields. The pattern every function uses: fetch rows
   eagerly in `NewState`, store them as plain exported slices (`Entities
   []Entity`, `Names []string`, `Rows []PropertyRow`) plus a `Done bool`, and
   **rebuild the Arrow batch in `Process`**. `TestRegisterDoesNotPanic` guards
   this.

2. **`haybarn-unittest` silently SKIPS `require vgi`.** Under haybarn the
   extension is not autoloaded for `require`, so a `.test` using `require vgi`
   is skipped (looks green but ran nothing). Use an explicit `statement ok` /
   `LOAD vgi;` instead — every `.test` here does.

3. **The `json` extension is NOT autoloaded under haybarn either.** To use
   `json_extract_string` on the `entity` column in a `.test`, do an explicit
   `INSTALL json; LOAD json;` first. (Other assertions use `LIKE` on the raw
   JSON string so they need no extension.)

4. **`select` and `filter` are SQL keywords.** As named table-function args they
   must be **quoted identifiers** in SQL: `"select" := …`, `"filter" := …`.
   Unquoted `select :=` is a parser error. The wire/arg names are still plain
   `select` / `filter` (set via `name=` in the struct tag).

5. **No hangs.** The package `http.Client` has a 30s timeout, and `Query` caps
   both total entities (`max_rows`, default 10000) and total HTTP requests
   (100k) so a non-advancing `nextLink` can never loop forever.

## v2 vs v4 response handling

`odata_query`'s `version` arg selects the JSON shape decoded in
`client.go:decodePage`:

- **v4** (default): collection = top-level `value` array; next link =
  `@odata.nextLink`.
- **v2**: payload nested under `d`; collection = `d.results` (with a fallback to
  `d` being the array itself for the `{"d":[...]}` form); next link = `d.__next`.

Paging (`Query`): each next link is resolved against the current page URL
(`resolveNext`, so relative links work) and fetched until empty or `max_rows`.
`$filter`/`$select`/`$orderby`/`$top` are applied as URL query params on the
**first** request only; subsequent requests follow the server's next link
verbatim (which already encodes them). `odata_entity_sets` reads `value[].name`
from the service document; `odata_metadata` XML-unmarshals the `$metadata` EDMX
namespace-agnostically (matching on local element names) so it parses both EDM
3.0 (v2) and EDM 4.0 (v4).

## Mock-server E2E (how `make test-sql` works)

Mirrors `vgi-grpc`'s start/stop pattern:

1. `make build` compiles `vgi-odata-worker` **and** `mockserver`.
2. `mockserver --addr 127.0.0.1:0` binds a free port and prints `PORT:<n>`; the
   Makefile captures it.
3. The Makefile exports `VGI_ODATA_WORKER` (the worker binary, used as the
   ATTACH `LOCATION`) and `VGI_ODATA_TEST_URL=http://127.0.0.1:<n>` (read by the
   `.test` files as the service root).
4. `haybarn-unittest --test-dir . "test/sql/*"` runs the suite.
5. A shell `trap` kills the mock; the mock handles SIGTERM with
   `http.Server.Shutdown`, so it exits 0 (clean `make`).

`cmd/mockserver` and the httptest unit tests share `internal/odatamock.New()`,
which serves a v4 service document, a two-page `/People` set (4 entities total,
with `@odata.nextLink`), and a `/$metadata` EDMX doc. The mock records the
first-page request's query so a unit test can assert option pass-through.

## Test inventory

- **Go (`make test-unit`)** — `internal/odataworker/*_test.go`: `odata_query`
  across both pages, `max_rows` cap, `$filter`/`$select`/`$top`/`$orderby`
  pass-through (asserted via the mock's recorded query), `odata_entity_sets`,
  `odata_metadata` EDMX parse, a **v2-shape** (`d.results`/`d.__next`) paging
  test, and errors (404/500/bad JSON/bad XML/bad URL/unreachable), plus the VGI
  `NewState` data path, NULL→no-rows, and the registration gob-encodability
  guard.
- **SQL (`make test-sql`)** — `test/sql/odata_query.test` (count across pages,
  raw-JSON `LIKE`, `json_extract_string`, `max_rows`, options, NULL, errors) and
  `test/sql/odata_discovery.test` (`odata_entity_sets`, `odata_metadata`, NULL,
  error).

## Conventions

- Source files start with `// Copyright 2026 Query Farm LLC - https://query.farm`.
- `gofmt`, `go vet`, and `go test ./...` must be clean before committing.
- See `README.md` for the prominent note on overlap with the `erpl_web`
  community extension and `vgi-odata`'s positioning as a generic OData reader.
```
