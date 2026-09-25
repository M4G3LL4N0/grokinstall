# Example: a documentation source

Demonstrates turning documentation into a bounded local capability instead of
loading a documentation set into model context.

**Input**

A directory containing `README.md` and `docs/*.md`.

**Command**

```bash
grokinstall install ./docs \
  --goal "Let GrokBot retrieve relevant documentation"
```

**Decision**

When the goal is documentation retrieval, `knowledge_import` is recommended even
if the project also ships a CLI. Indexing the docs is smaller and more faithful
than exposing a command.

**Result**

```text
Strategy:     knowledge_import (supported)
Adapter:      ~/.grokinstall/adapters/project.docs/index.json
Verification: index loads, has content, search responds
```

**Call it**

```bash
grokinstall run project.docs --input '{"query":"authentication","limit":3}'
```

```json
{
  "query": "authentication",
  "matches": [
    {
      "file": "docs/auth.md",
      "title": "Authentication",
      "excerpt": "Use a bearer token in the Authorization header…",
      "score": 12.4
    }
  ],
  "total_documents": 64
}
```

**Bounds**

- indexing is capped by file count, per-file size and total size
- binary content is rejected
- excerpts are bounded
- no embeddings, no vector database: the ranking is a plain term-frequency
  score you can explain

**This is a local capability.** Documentation is copied into a bounded index at
install time; the original files are never modified, and uninstall removes the
index GrokInstall created.
