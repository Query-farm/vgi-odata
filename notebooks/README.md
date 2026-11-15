# OData v2/v4 Reader: SQL notebooks

Read OData v2/v4 REST services from SQL, with $filter pushdown, automatic paging and metadata discovery.

| Notebook | Question | Controlled experiment |
| --- | --- | --- |
| [A row cap is not an inventory total](01_paging_caps_and_stock_gaps.ipynb) | How do delivered HTTP pages, retained SQL rows and known stock gaps differ? | Change `MAX_ROWS` from 8 to 4. |
| [A status flag changes the population](02_a_status_flag_changes_the_population.ipynb) | Does excluding discontinued products remove only empty stock rows? | Change `INCLUDE_DISCONTINUED` from `True` to `False`. |

Each notebook has an **Open in Colab** link, visible connection SQL, executed tables, interpretations and vector charts. GitHub does not honor editor cell-folding metadata; setup is kept short. Editors that support folding can collapse supporting Python cells while the release connection stays visible.

## Run locally or in Colab

Open the notebook in Colab and run all cells, or use [uv](https://docs.astral.sh/uv/) from this directory:

```bash
uv sync --locked
uv run --locked vgi-notebooks
```

The visible connection uses the published native release:

```sql
ATTACH 'odata' (
  TYPE vgi,
  LOCATION 'github-auto://Query-farm/vgi-odata@v0.4.0'
);
```

The extension downloads the platform archive and verifies its SHA-256 sidecar. First use needs network access to GitHub, package indexes and extension downloads; no Go build or worker checkout is required. GitHub's anonymous release API can rate-limit a cold download. Once cached, the release is reused.

Shared setup and presentation come from [vgi-example-notebooks v0.3.49](https://github.com/Query-farm/vgi-example-notebooks/tree/v0.3.49). Notebook-only execution retrieves the eight worker companions at an immutable public revision and verifies their hashes. The bootstrap installs the shared base package and retains compatible preloaded libraries; the local environment separately includes the runner extra. Python 3.13/3.14 is supported here. The shared [standard](https://github.com/Query-farm/vgi-example-notebooks/blob/main/docs/standard.md) and executor are maintained centrally.

For local interactive use, select this environment's interpreter or register it:

```bash
uv run --locked python -m ipykernel install --user --name vgi-odata-notebooks --display-name 'Python (vgi-odata notebooks)'
```

Restart and run all after changing parameters. Save experiments separately:

```bash
uv run --locked vgi-notebooks 01_paging_caps_and_stock_gaps.ipynb --parameters '{"MAX_ROWS":4}' --output-dir validation/cap-four
uv run --locked vgi-notebooks 02_a_status_flag_changes_the_population.ipynb --parameters '{"INCLUDE_DISCONTINUED":false}' --output-dir validation/status-active
uv run --locked vgi-notebooks 02_a_status_flag_changes_the_population.ipynb --parameters '{"DATA_MODE":"live"}' --output-dir validation/live
```

## Data and interpretation

The first story preserves the original eight invented products, targets and NULL stock value in [fixtures](fixtures/provenance.json). A loopback-only, read-only fixture supplies fixed three-entity pages with relative continuation links. The real worker performs HTTP reads; a fixture request log provides additional visibility that a general service need not expose. Reducing the cap from eight to four reduces known gap from 39 to 8 units. Two requests deliver six entities, of which four are retained. Missing stock stays NULL and an omitted category is labeled not retrieved. The stock targets are demonstration inputs, not demand forecasts or order recommendations.

The second story reads the worker's built-in public-service registry and selects Northwind V4 before discovering Products and EDM property types. Its default replays three complete, unchanged public HTTP responses: service document, metadata and the first twenty ProductID-ordered products. [Provenance](recordings/provenance.json) records request URLs, capture times and hashes. Northwind is provider-authored sample data, **not real enterprise inventory**. Live mode makes the same explicit public reads and never falls back to replay.

The twenty sample products contain three discontinued items with 29 stock units, including one nonzero stock count. Excluding them changes the selected population from 20 to 17 products and its known stock sum from 665 to 636 units. All source keys and values stay fixed. Unit prices and stock counts have separate meanings; neither a row cap nor a status flag establishes a complete inventory total. Registry, metadata and product requests are separate reads, not an atomic snapshot.

## Validation and coverage

Both saved defaults and both detached experiments passed with the actual native v0.4.0 release. Twelve paging SQL invariants and eight status SQL invariants passed, with schema/request/population assertions. The original five displayed tables, original fixture bytes and synthetic service helper are preserved. Six runner guards reject unsafe experiment destinations or unknown parameters; nine invalid parameter cases are rejected. A bounded anonymous live run reproduced the recorded public tables.

A fresh notebook-only Python 3.13 kernel retained preloaded pandas 2.2.3, Matplotlib 3.10.0, IPython 8.30.0, ipykernel 6.17.1 and jupyter-client 7.4.9, installed only the shared base package, downloaded verified companions and completed all cells with a clean dependency check. Its native worker came from the existing verified cache: an attempted cold release lookup returned GitHub HTTP 403. This is local Colab compatibility evidence; the hosted Colab UI was not manually exercised.

[Coverage evidence](coverage.json) includes the complete three-function catalog and `odata_public_services` view: **4/4 released and 4/4 current-compatible surfaces**. Exact current-main runtime coverage is 0; current registration and implementation were reviewed against the release, with metadata/example/test changes only. Existing green source CI at the unchanged public source revision was reused; no worker rebuild or new full source suite was needed. Optional bearer authentication and OData v2 services are not exercised by these anonymous v4 stories.

Saved outputs include native SVG and content-addressed SVG assets for GitHub. Review PNGs, request logs and detached experiments are ignored. [Worker repository](https://github.com/Query-farm/vgi-odata) · [Query.Farm](https://query.farm/) · [VGI](https://query.farm/vgi/)
