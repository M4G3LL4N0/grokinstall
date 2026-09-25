# Strategies

GrokInstall always compares every strategy for a source and goal. It never
silently picks one, and `no_install` is always in the comparison.

```bash
grokinstall compare SOURCE --goal "..." [--json]
```

## The ten strategies

| Strategy | Idea | Status |
| --- | --- | --- |
| `cli_bridge` | wrap an existing command behind a JSON contract | supported |
| `knowledge_import` | bounded local index, deterministic search | supported |
| `external_execution` | register an externally managed command | supported |
| `no_install` | use what already exists; install nothing | supported |
| `api_bridge` | call a documented HTTP surface | plan-only |
| `mcp_bridge` | connect an MCP server | plan-only |
| `docker_bridge` | run a detected container | plan-only |
| `local_service` | manage a long-running local process | plan-only |
| `generated_adapter` | generate an adapter for a surface with no bridge | plan-only |
| `micro_prompt_pack` | a small instruction pack instead of an install | plan-only |

Plan-only strategies are **detected today**. Full installation support is coming
next. They are persisted as plans in `~/.grokinstall/plans/`, never registered as
capabilities, and they refuse to run.

## The twelve dimensions

Every applicable strategy is assessed on all twelve, each with a reason:

```text
grokbot_footprint   feature_coverage    local_execution    external_cost
latency             maintenance         security           privacy
cacheability        dependencies        implementation_effort
portability
```

Ratings are qualitative: `excellent`, `good`, `fair`, `poor`, `not_applicable`.
GrokInstall does not invent percentages, latency numbers or savings. If it has
not measured something, it does not claim it.

## How a recommendation is reached

The recommendation is deterministic and inspectable:

1. **The goal decides first.** A project with both a CLI and documentation, asked
   to "retrieve relevant documentation", gets `knowledge_import` — indexing the
   docs is smaller and more faithful than exposing a command. An explicitly
   operational goal still gets the CLI.
2. **Capability decides next.** A usable CLI capability is the smallest bridge.
3. **Declared surfaces next.** MCP, then API, then Docker, then a local service.
4. **Otherwise, do not install.** If nothing callable exists, the honest answer
   is `no_install`, backed by a receipt.

## Comparing honestly

Detection is never blurred with installation:

- a plan-only strategy registers nothing
- a supported strategy that cannot obtain an executable **fails** rather than
  degrading to a plan
- `grokinstall capabilities` shows runnable capabilities only, by default

## Adding a strategy

A new strategy must be added to the comparison set so it is always considered,
declare its support level honestly, and be tested both for the cases where it
applies and for the case where it must not be advertised as runnable.

See [CONTRIBUTING.md](../CONTRIBUTING.md).
