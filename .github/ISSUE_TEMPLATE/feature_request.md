---
name: Feature request
about: Suggest a capability or improvement
title: "[feature] "
labels: enhancement
assignees: ''
---

## The problem

<!-- What can't you do today? Describe the situation, not the solution. -->

## What you would like

<!-- The capability or behaviour you want. -->

## Why this fits GrokInstall

<!-- Which part of the promise does this serve: smaller capability, safer
     provisioning, better diagnosis, clearer contracts? -->

## Which strategy or provisioner

If this relates to one of the currently detected-but-plan-only strategies
(`api_bridge`, `mcp_bridge`, `docker_bridge`, `local_service`,
`generated_adapter`, `micro_prompt_pack`), please say which.

## Alternatives you considered

## Scope check

- [ ] This keeps GrokBot a thin orchestrator, rather than sending it more source.
- [ ] This does not require GrokInstall to become a general package manager.
- [ ] This can be implemented deterministically, or the reasoning part is
      explicitly optional and not required for the common path.
