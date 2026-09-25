package adapter

import "encoding/json"

// marshal and decodeArray are small indirections so the adapter's dependency on
// encoding/json stays in one place.
func marshal(v any) ([]byte, error) { return json.Marshal(v) }

func decodeArray(data []byte) ([]string, error) {
	var items []any
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, inputValue(item).Str)
	}
	return out, nil
}

// Normalize converts upstream stdout into the GrokInstall result value implied
// by the adapter's output mode.
//
// The rule is honesty over convenience: a CLI that prints text yields text, and
// only a CLI whose output really is JSON yields structured JSON. Nothing here
// invents a shape the upstream did not produce.
func Normalize(mode, field string, stdout []byte, exitCode int, jsonExpected bool) (any, error) {
	switch mode {
	case OutputJSON:
		trimmed := trimSpace(stdout)
		if len(trimmed) == 0 {
			if jsonExpected {
				return nil, &Error{Code: CodeMalformedOutput, Message: "capability produced no output but JSON was expected"}
			}
			return map[string]any{}, nil
		}
		var v any
		if err := json.Unmarshal(trimmed, &v); err == nil {
			return v, nil
		}
		if jsonExpected {
			return nil, &Error{Code: CodeMalformedOutput, Message: "capability declared JSON output but produced non-JSON"}
		}
		// The upstream did not honour JSON output. Report the text honestly
		// instead of pretending it parsed.
		return map[string]any{"text": string(trimmed)}, nil
	case OutputText:
		return map[string]any{field: string(stdout)}, nil
	case OutputLines:
		return map[string]any{field: splitLines(string(stdout))}, nil
	case OutputExitStatus:
		return map[string]any{"exit_code": exitCode}, nil
	case OutputArtifact:
		return map[string]any{"output": string(stdout)}, nil
	default:
		return nil, &Error{Code: CodeInvalidAdapter, Message: "unsupported output mode " + mode}
	}
}

// Error codes surfaced by output normalization.
const (
	CodeInvalidAdapter  = "invalid_adapter"
	CodeMalformedOutput = "malformed_output"
)

// Error is a structured adapter failure.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
