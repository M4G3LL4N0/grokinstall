package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const echoJSON = `#!/bin/sh
cat > /dev/null
printf '{"ok":true,"result":{"echo":"hi"}}'
`

func TestSubprocessEchoesJSONInput(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type: TypeSubprocess,
		Command: script(t, "echo.sh", `#!/bin/sh
read line
printf '{"ok":true,"result":{"received":%s}}' "$line"
`),
		InputMode: ModeStdinJSON,
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{"query":"foo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("expected ok, got %+v", resp)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "foo") {
		t.Fatalf("input did not reach the subprocess: %s", raw)
	}
}

func TestArgvModePassesInputAsArguments(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type: TypeSubprocess,
		Command: script(t, "args.sh", `#!/bin/sh
printf '{"ok":true,"result":{"argc":%d,"arg1":"%s"}}' "$#" "$2"
`),
		InputMode: ModeArgv,
		ArgvMap:   map[string]string{"query": "--query"},
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{"query":"hello world"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "hello world") {
		t.Fatalf("argv input not passed: %s", raw)
	}
	if !strings.Contains(string(raw), `"argc":2`) {
		t.Fatalf("expected two argv entries: %s", raw)
	}
}

func TestNoShellInterpolation(t *testing.T) {
	// A shell metacharacter must arrive as literal data, never be interpreted.
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type: TypeSubprocess,
		Command: script(t, "safe.sh", `#!/bin/sh
printf '{"ok":true,"result":{"value":"%s"}}' "$2"
`),
		InputMode: ModeArgv,
		ArgvMap:   map[string]string{"q": "--q"},
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{"q":"; touch /tmp/grokinstall-pwned"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "; touch") {
		t.Fatalf("metacharacters must stay literal: %s", raw)
	}
	if _, err := os.Stat("/tmp/grokinstall-pwned"); err == nil {
		t.Fatal("shell interpolation happened: a file was created")
	}
}

func TestTimeoutIsEnforced(t *testing.T) {
	r := New()
	start := time.Now()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   script(t, "slow.sh", "#!/bin/sh\ncat > /dev/null\nsleep 10\n"),
		InputMode: ModeNone,
		Timeout:   300 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("a timed-out capability must not report success")
	}
	if resp.Error == nil || resp.Error.Code != CodeTimeout {
		t.Fatalf("expected timeout error, got %+v", resp.Error)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout did not interrupt the process")
	}
}

func TestOutputIsBounded(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:           TypeSubprocess,
		Command:        script(t, "loud.sh", "#!/bin/sh\ncat > /dev/null\ni=0\nwhile [ $i -lt 5000 ]; do printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; i=$((i+1)); done\n"),
		InputMode:      ModeNone,
		Timeout:        5 * time.Second,
		MaxOutputBytes: 1024,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("oversized output must fail rather than be silently truncated")
	}
	if resp.Error == nil || resp.Error.Code != CodeOutputTooLarge {
		t.Fatalf("expected output-too-large error, got %+v", resp.Error)
	}
}

func TestNonZeroExitIsStructuredFailure(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   script(t, "fail.sh", "#!/bin/sh\ncat > /dev/null\nprintf 'bad input' >&2\nexit 4\n"),
		InputMode: ModeStdinJSON,
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("non-zero exit must not report success")
	}
	if resp.Error.Code != CodeNonZeroExit {
		t.Fatalf("code = %q, want %s", resp.Error.Code, CodeNonZeroExit)
	}
	if !strings.Contains(resp.Error.Message, "bad input") {
		t.Fatalf("stderr should surface: %q", resp.Error.Message)
	}
}

func TestMalformedOutputIsStructuredFailure(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:       TypeSubprocess,
		Command:    script(t, "bad.sh", "#!/bin/sh\ncat > /dev/null\nprintf 'not json at all'\n"),
		InputMode:  ModeStdinJSON,
		Timeout:    5 * time.Second,
		ExpectJSON: true,
	}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeMalformedOutput {
		t.Fatalf("expected malformed-output error, got %+v", resp.Error)
	}
}

func TestPlainTextOutputIsWrapped(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   script(t, "text.sh", "#!/bin/sh\ncat > /dev/null\nprintf 'plain result line'\n"),
		InputMode: ModeStdinJSON,
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("plain text is a valid result: %+v", resp)
	}
	if resp.Result == nil {
		t.Fatal("result should carry the text output")
	}
}

func TestMissingExecutableIsReported(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   filepath.Join(t.TempDir(), "nope"),
		InputMode: ModeNone,
		Timeout:   time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeSpawnFailed {
		t.Fatalf("expected spawn failure, got %+v", resp.Error)
	}
}

func TestUnsupportedExecutionTypeIsReported(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{Type: "plan_only", Command: "anything"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeNotExecutable {
		t.Fatalf("expected not-executable, got %+v", resp.Error)
	}
}

func TestInvalidInputIsRejected(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   script(t, "ok.sh", echoJSON),
		InputMode: ModeStdinJSON,
		Timeout:   5 * time.Second,
	}, json.RawMessage(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidInput {
		t.Fatalf("expected invalid-input, got %+v", resp.Error)
	}
}

func TestWorkingDirIsApplied(t *testing.T) {
	dir := t.TempDir()
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:       TypeSubprocess,
		Command:    script(t, "pwd.sh", "#!/bin/sh\nprintf '{\"ok\":true,\"result\":{\"cwd\":\"%s\"}}' \"$(pwd)\"\n"),
		InputMode:  ModeNone,
		WorkingDir: dir,
		Timeout:    5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), filepath.Base(dir)) {
		t.Fatalf("working dir not applied: %s", raw)
	}
}

func TestEnvironmentIsExplicit(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:      TypeSubprocess,
		Command:   script(t, "env.sh", "#!/bin/sh\nprintf '{\"ok\":true,\"result\":{\"token\":\"%s\"}}' \"$GI_TOKEN\"\n"),
		InputMode: ModeNone,
		Env:       map[string]string{"GI_TOKEN": "abc123"},
		Timeout:   5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "abc123") {
		t.Fatalf("explicit environment not applied: %s", raw)
	}
}

func TestBuiltinHandlerReceivesInput(t *testing.T) {
	r := New()
	r.Register("test", func(ctx context.Context, input json.RawMessage) (any, error) {
		return map[string]any{"handled": string(input)}, nil
	})
	resp, err := r.Invoke(contextTODO(), Spec{
		Type:    TypeBuiltin,
		Handler: "test",
		Timeout: 5 * time.Second,
	}, json.RawMessage(`{"query":"abc"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("builtin should succeed: %+v", resp)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "abc") {
		t.Fatalf("builtin did not receive input: %s", raw)
	}
}

func TestUnknownBuiltinHandler(t *testing.T) {
	r := New()
	resp, err := r.Invoke(contextTODO(), Spec{Type: TypeBuiltin, Handler: "nope"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeSpawnFailed {
		t.Fatalf("expected failure for unknown handler, got %+v", resp.Error)
	}
}
