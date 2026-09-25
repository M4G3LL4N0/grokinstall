---
name: Integration request
about: Ask GrokInstall to support a specific tool or service
title: "[integration] "
labels: integration
assignees: ''
---

## What do you want to integrate

- Tool / service / repository:
- Link, if public:
- Ecosystem (CLI, HTTP API, MCP server, container, package, docs, something else):

## What should GrokBot be able to do

<!-- A concrete goal, in the form:
     "Let GrokBot use this to ..."
     This matters: a plan is not an install, and "install everything" is
     almost never the right answer. -->

## What evidence exists in the source

If you have looked, what does GrokInstall detect today?

```bash
grokinstall inspect <source> --json
grokinstall compare <source> --goal "..." --json
```

## How would you expect it to be integrated

- [ ] Existing CLI (wrap the command)
- [ ] HTTP API (API bridge — detected today, plan-only)
- [ ] MCP server (MCP bridge — detected today, plan-only)
- [ ] Container (Docker bridge — detected today, plan-only)
- [ ] Local service (detected today, plan-only)
- [ ] Knowledge / documentation only
- [ ] No install: an existing external interface is enough
- [ ] Not sure — I want GrokInstall's recommendation

## Release artifacts

If this is a public GitHub project, does it publish release binaries for common
platforms, and does it publish checksums? GrokInstall can provision a release
artifact safely; without one it will look for other routes and may require
authorization it cannot get automatically.

## Security notes

Does installing or running this tool require lifecycle scripts, elevated
privileges, sudo, or a global package-manager change?
