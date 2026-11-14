# OData SQL notebooks

[A row cap is not an inventory total](01_paging_caps_and_stock_gaps.ipynb) discovers an OData entity set and EDM metadata, casts raw entity JSON in SQL, and compares known stock gaps under a row cap. A local HTTP request audit distinguishes entities delivered by the service from rows retained by the worker.

## Run from this directory

Install [uv](https://docs.astral.sh/uv/) and Python 3.13 or 3.14. First use needs network access for notebook dependencies, published Haybarn/VGI extensions and the pinned native worker release. Copy this entire directory to run it independently; no worker source checkout or Go build is required.

```bash
uv sync --frozen
uv run --frozen python execute.py
```

The visible SQL attaches `github-auto://Query-farm/vgi-odata@v0.4.0`. Haybarn/VGI downloads the platform archive, verifies the SHA-256 sidecar and caches it. The notebook environment is separately locked to Haybarn 2.0.0rc11. Change the literal release pin to upgrade, then repeat default, experiment and invariant checks.

For interactive use, register this interpreter and select it in Jupyter:

```bash
uv run --frozen python -m ipykernel install --user --name vgi-odata-notebooks --display-name 'Python (vgi-odata notebooks)'
```

Restart and run all after changing the visible parameter. Python setup, helpers and plotting are folded; SQL, tables, charts and interpretations remain visible. The runner uses a temporary kernel and this directory as cwd. It saves only after success, requires a separate destination for overrides, and rejects unknown parameters or an output directory equal to the source.

## Focused experiment

```bash
uv run --frozen python execute.py --parameters '{"MAX_ROWS":4}' --output-dir validation/cap-four
```

Change only `MAX_ROWS` from 8 to 4. The worker follows the fixture's relative `@odata.nextLink` values and retains at most the cap, even when an HTTP page contains more entities. `$top` is not used. The reference cap of 100 exceeds the eight-product fixture. Both scans request stable `ProductID` ordering; `seq` is an output ordinal, not the product key.

The fixture binds only loopback, implements read-only discovery/metadata/product routes, and uses [invented products](fixtures/products.json) with [explicit provenance](fixtures/provenance.json). It is intentionally a narrow OData v4 service, not a general server implementation. Its fixed page size is three. The worker still performs real HTTP calls and returns Arrow rows through VGI. No enterprise credentials or real inventory is involved.

The helper's phase labels tag the local request log; they do not change response content. This audit is extra fixture visibility, not an output column from `odata_query`. It records page offsets, entities served and continuation availability. A general service may not expose this evidence. Materialized worker calls prevent DataFrame display from repeating fetches.

## Validation and interpretation

Twelve SQL checks cover discovery, metadata, complete fixture coverage, unique business keys, exact capped-prefix retention, nullable values, stock-gap arithmetic and request/retention reconciliation. Stock `null` remains unavailable, not zero stock or zero gap. Targets are invented counts, not a demand forecast; the calculation is not an order recommendation. A capped aggregate is not automatically a full-service total, and exactly reaching the cap does not prove completeness.

The synthetic default and detached experiment passed through the pinned native release. Changing the cap from 8 to 4 reduces retained rows from 8 to 4 and known gap from 39 to 8 units. The selected request count drops from three to two; six entities arrive over HTTP and four are kept. The minimum supported cap of 1 also passed all checks, including empty unknown-stock accounting. A separate read-only probe also verified public Northwind product retrieval and EDM metadata through the same release. Repository gates passed: 21 top-level Go tests, build/vet/gofmt/golangci-lint, 31 stdio / 35 HTTP / 31 Unix SQL assertions, and metadata 100/100, L2 behavioral, with zero findings.

The saved notebook embeds successful default outputs. Logs, request audits, review charts and experiment copies are ignored under `validation/`. Runtime companions, fixtures and official assets are copied alongside experiment outputs by the runner. [STANDARD.md](STANDARD.md) records presentation conventions.
