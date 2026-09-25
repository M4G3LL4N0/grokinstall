package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/contextpack"
)

func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const echoWorker = `#!/bin/sh
input=$(cat)
printf '{"ok":true,"result":{"echo":%s},"artifacts":[],"warnings":[]}' "$(printf '%s' "$input" | sed 's/.*\"task\":\"\\([^\"]*\\)\".*/\\1/')"
`

func TestSubprocessWorkerRoundTrip(t *testing.T) {
	w := NewSubprocess(writeScript(t, "ok.sh", `#!/bin/sh
cat > /dev/null
printf '{"ok":true,"result":{"did":"work"},"artifacts":["a.txt"],"warnings":[]}'
`))
	res, err := w.Execute(context.Background(), Request{Task: "do the thing", Input: map[string]any{"x": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal("expected ok response")
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0] != "a.txt" {
		t.Fatalf("artifacts = %v", res.Artifacts)
	}
}

func TestRequestCarriesContextPack(t *testing.T) {
	pack := contextpack.New().WithGoal("diagnose repository")
	req := Request{Task: "review", ContextPack: pack}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "context_pack") {
		t.Fatalf("request JSON missing context pack: %s", data)
	}
	var back Request
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ContextPack == nil || back.ContextPack.Goal != "diagnose repository" {
		t.Fatalf("context pack lost in round trip: %+v", back.ContextPack)
	}
}

func TestMalformedWorkerResponseIsAnError(t *testing.T) {
	w := NewSubprocess(writeScript(t, "bad.sh", `#!/bin/sh
cat > /dev/null
printf 'this is not json'
`))
	_, err := w.Execute(context.Background(), Request{Task: "x"})
	if err == nil {
		t.Fatal("expected an error for malformed worker output")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error should name the problem: %v", err)
	}
}

func TestWorkerTimeout(t *testing.T) {
	w := NewSubprocess(writeScript(t, "slow.sh", `#!/bin/sh
cat > /dev/null
sleep 5
`))
	w.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := w.Execute(context.Background(), Request{Task: "x"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not interrupt the worker")
	}
}

func TestOutputIsBounded(t *testing.T) {
	w := NewSubprocess(writeScript(t, "loud.sh", `#!/bin/sh
cat > /dev/null
i=0
while [ $i -lt 2000 ]; do
  printf '{"ok":true,"result":{"pad":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}'
  i=$((i+1))
done
`))
	w.MaxOutputBytes = 512
	_, err := w.Execute(context.Background(), Request{Task: "x"})
	if err == nil {
		t.Fatal("expected bounded output to be rejected rather than accepted")
	}
	if !strings.Contains(err.Error(), "output") {
		t.Fatalf("error should mention output bounds: %v", err)
	}
}

func TestFailingWorkerReportsStderr(t *testing.T) {
	w := NewSubprocess(writeScript(t, "fail.sh", `#!/bin/sh
cat > /dev/null
printf 'boom: missing dependency' >&2
exit 3
`))
	_, err := w.Execute(context.Background(), Request{Task: "x"})
	if err == nil {
		t.Fatal("expected error from failing worker")
	}
	if !strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("stderr should surface: %v", err)
	}
}

func TestRegistryLookupUnknownWorker(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Lookup("nope"); err == nil {
		t.Fatal("expected error for unknown worker")
	}
}

func TestRegistryReportsUnavailableOptionalProviders(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"opencode", "cursor", "chatgpt", "claude", "grok", "ollama"} {
		p, err := reg.Lookup(name)
		if err != nil {
			t.Fatalf("%s should be a known provider in Part 1: %v", name, err)
		}
		ok, reason := p.Available()
		if ok {
			t.Fatalf("%s unexpectedly reported available", name)
		}
		if reason == "" {
			t.Fatalf("%s must explain why it is unavailable", name)
		}
	}
}

func TestSubprocessWorkerAvailable(t *testing.T) {
	w := NewSubprocess("/bin/sh")
	ok, _ := w.Available()
	if !ok {
		t.Fatal("/bin/sh should be available")
	}
	w2 := NewSubprocess(filepath.Join(t.TempDir(), "missing"))
	if ok, reason := w2.Available(); ok || reason == "" {
		t.Fatal("missing worker must report unavailable with a reason")
	}
}
