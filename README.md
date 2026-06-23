<p align="center">
  <img src="https://raw.githubusercontent.com/Query-farm/vgi/main/docs/vgi-logo.png" alt="Vector Gateway Interface (VGI)" width="320">
</p>

<p align="center"><em>A <a href="https://query.farm">Query.Farm</a> VGI worker for DuckDB.</em></p>

# Query OData v2/v4 Services as Tables in DuckDB

> **vgi-odata** · a [Query.Farm](https://query.farm) VGI worker

[![CI](https://github.com/Query-farm/vgi-odata/actions/workflows/ci.yml/badge.svg)](https://github.com/Query-farm/vgi-odata/actions/workflows/ci.yml)

A [VGI](https://query.farm) worker, written in **Go**, that queries **OData**
v2/v4 services from DuckDB/SQL and returns their entities as rows. OData is the
REST/JSON protocol behind **Microsoft Dynamics 365 / Dataverse**, **SAP
Gateway** (S/4HANA, NetWeaver), and a large class of other enterprise APIs, so
`vgi-odata` is a **generic OData reader** rather than a vendor-specific
connector.

Entities come back as their raw JSON object text in a single `entity` column;
pull fields out with DuckDB's `json_extract` / `json_extract_string`.

```sql
INSTALL vgi FROM community; LOAD vgi;
INSTALL json; LOAD json;     -- to json_extract from the entity column

-- LOCATION is the path to the compiled worker binary.
ATTACH 'odata' AS odata (TYPE vgi, LOCATION '/path/to/vgi-odata-worker');

-- Discover the entity sets a service exposes.
SELECT * FROM odata.odata_entity_sets('https://services.odata.org/V4/TripPinService');

-- Read an entity set; one row per entity, `entity` is the raw JSON object.
SELECT seq,
       json_extract_string(entity, '$.UserName')  AS user_name,
       json_extract_string(entity, '$.FirstName') AS first_name
FROM odata.odata_query(
       'https://services.odata.org/V4/TripPinService', 'People',
       top := '5');

-- Inspect the schema from $metadata (EDMX).
SELECT * FROM odata.odata_metadata('https://services.odata.org/V4/TripPinService')
WHERE entity_type = 'Person';
```

## Note on overlap with `erpl_web`

The community DuckDB extension
[**`erpl_web`**](https://github.com/Query-farm/erpl) ships SAP/OData
integration (and OData v2/v4 read support) aimed primarily at the **SAP**
ecosystem. `vgi-odata` deliberately does **not** try to replace it. Instead it
positions itself as a small, **generic v2/v4 OData reader** built on the VGI
worker model:

- **Generic, not SAP-specific.** No SAP RFC/BAPI, no Dynamics auth flows — just
  HTTP + JSON + EDMX. It works against TripPin, Dynamics Web API, SAP Gateway,
  Northwind, or any spec-compliant OData service.
- **Raw-JSON passthrough.** Entities are returned verbatim as JSON strings; you
  decide which fields to project with `json_extract`, instead of the worker
  imposing a fixed column model.
- **A Go VGI worker.** It runs as a separate process over the VGI protocol and
  is independent of the C++ extension build.

If your use case is deep SAP integration, prefer `erpl_web`. If you want a
lightweight, dependency-free OData v2/v4 reader for arbitrary services, use
`vgi-odata`.

## Functions

All three are **table functions** (each makes HTTP requests and returns rows).
The first argument is always the OData service root URL.

| Function | Returns | Description |
| --- | --- | --- |
| `odata_query(service_url, entity_set)` | `seq BIGINT, entity VARCHAR` | One row per entity; `entity` is the raw JSON object. Follows nextLink paging to completion (bounded by `max_rows`). |
| `odata_entity_sets(service_url)` | `name VARCHAR` | Entity-set names from the service document (`value[].name`). |
| `odata_metadata(service_url)` | `entity_type VARCHAR, property VARCHAR, type VARCHAR` | One row per entity-type property, parsed from the `$metadata` EDMX XML. |

### `odata_query` named options

Named arguments use DuckDB's `name := value` syntax. Note that `select` and
`filter` are SQL keywords, so quote them as identifiers (`"select" := …`,
`"filter" := …`) when calling.

| Option | Default | Meaning |
| --- | --- | --- |
| `"filter"` | `NULL` | `$filter` system query option. |
| `"select"` | `NULL` | `$select` system query option. |
| `orderby` | `NULL` | `$orderby` system query option. |
| `top` | `NULL` | `$top` system query option (passed through verbatim). |
| `max_rows` | `10000` | Maximum entities to collect across all pages (paging cap). |
| `token` | `NULL` | Bearer token, sent as `Authorization: Bearer <token>`. |
| `version` | `'v4'` | Response shape: `'v4'` (`value` / `@odata.nextLink`) or `'v2'` (`d.results` / `d.__next`). |

```sql
SELECT json_extract_string(entity, '$.UserName') AS user_name
FROM odata.odata_query(
       'https://services.odata.org/V4/TripPinService', 'People',
       "filter" := 'FirstName eq ''Russell''',
       "select" := 'UserName,FirstName',
       top      := '10',
       token    := 'eyJ...',
       version  := 'v4');
```

## OData v2 vs v4

The protocol comes in two JSON shapes; `version` selects which the worker
expects:

- **v4** (`version := 'v4'`, the default): the entity collection is the
  top-level `value` array and the continuation link is `@odata.nextLink`.
- **v2** (`version := 'v2'`): the payload is nested under `d`, the collection is
  `d.results` (or `d` itself for some services), and the continuation link is
  `d.__next`.

In both cases paging is followed automatically: each `nextLink` (relative links
are resolved against the page URL) is fetched until there is no next link or
`max_rows` entities have been collected. A request cap (100k pages) guards
against a non-advancing `nextLink` loop.

## Behavior & robustness

- **NULL / absent input → no rows.** A NULL `service_url` (or NULL `entity_set`
  for `odata_query`) yields zero rows rather than an error.
- **Clear errors, never a crash or hang.** A non-2xx HTTP status (4xx/5xx),
  malformed JSON, malformed EDMX XML, a non-absolute URL, or an unreachable host
  all surface as a clean DuckDB error. Every HTTP request is bounded by a 30s
  timeout, so an unreachable service fails fast.
- **`max_rows` cap.** Paging always stops at `max_rows` (default 10000), so a
  huge entity set can never exhaust memory unexpectedly.

## Build

Requires Go 1.25+.

```sh
make build        # builds ./vgi-odata-worker and ./mockserver
```

The `vgi-odata-worker` binary speaks the VGI protocol over stdio; point a DuckDB
`ATTACH ... (TYPE vgi, LOCATION '…')` at it.

## Test

```sh
make test-unit    # pure-Go unit tests (httptest mock OData service)
make test-sql     # haybarn-unittest SQL end-to-end against a local mock server
make test         # both
```

`make test-sql` needs [`haybarn-unittest`](https://query.farm) on `PATH`:

```sh
uv tool install haybarn-unittest
export PATH="$HOME/.local/bin:$PATH"
```

It builds the worker and a small **mock OData server** (`cmd/mockserver`,
serving a v4 service document, a two-page `/People` entity set, and a
`/$metadata` EDMX doc), starts it on a free port, points the SQL tests at it,
runs the suite, and stops the mock.

## Licensing

- This worker is licensed **MIT** — see [`LICENSE`](./LICENSE).
- It uses only the Go **standard library** (`net/http`, `encoding/json`,
  `encoding/xml`, `net/url`) for the OData/HTTP logic — no third-party OData SDK
  — plus the [`vgi-go`](https://github.com/Query-farm/vgi-go) SDK and Apache
  [arrow-go](https://github.com/apache/arrow-go) (**Apache-2.0**) for the VGI
  protocol and Arrow batches. See the `vgi-go` repo for its terms.

---

## Authorship & License

Written by [Query.Farm](https://query.farm).

Copyright 2026 Query Farm LLC - https://query.farm

