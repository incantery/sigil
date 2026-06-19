// Package trace is Sigil's minimal, dependency-free tracing model for
// scenario runs, plus an OTLP/JSON serializer. A scenario run is a trace;
// each step is a span. The same document is the AI's queryable artifact
// (structured JSON) and the human's dashboard source (push it to an
// OTLP-compatible backend — Tempo/Grafana — over HTTP).
//
// We hand-roll the OTLP/JSON encoding rather than pull in the
// OpenTelemetry SDK: a test run produces a small, bounded set of spans, so
// none of the SDK's batching/retry/context machinery earns its dependency
// weight. The output is the OTLP ExportTraceServiceRequest JSON shape that
// an OTLP/HTTP `/v1/traces` endpoint accepts.
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// Status mirrors OTLP's span status codes.
type Status int32

const (
	StatusUnset Status = 0
	StatusOK    Status = 1
	StatusError Status = 2
)

// Attr is one key/value attribute. Value must be string, int64, int, or
// bool; other types are rendered as their Go string form on marshal.
type Attr struct {
	Key   string
	Value any
}

// Event is a point-in-time annotation on a span (e.g. a captured binding).
type Event struct {
	Name      string
	TimeNanos int64
	Attrs     []Attr
}

// Span is one unit of work — a scenario (root) or a step (child).
type Span struct {
	Name       string
	SpanID     string // 16 lowercase hex chars (8 bytes)
	ParentID   string // 16 hex chars, or "" for a root span
	StartNanos int64
	EndNanos   int64
	Attrs      []Attr
	Status     Status
	StatusMsg  string
	Events     []Event
}

// Trace is one scenario run: a root span plus its step spans, all sharing
// the TraceID.
type Trace struct {
	TraceID string // 32 lowercase hex chars (16 bytes)
	Spans   []Span
}

// NewTraceID returns a random 16-byte trace id as 32 hex chars.
func NewTraceID() string { return randHex(16) }

// NewSpanID returns a random 8-byte span id as 16 hex chars.
func NewSpanID() string { return randHex(8) }

func randHex(n int) string {
	b := make([]byte, n)
	// crypto/rand.Read never returns a short read or error on supported
	// platforms; if it somehow did, a zero id is still a valid (if
	// useless) trace id and must not crash a test run.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MarshalOTLP renders the traces as an OTLP/JSON ExportTraceServiceRequest.
// serviceName populates the resource's service.name attribute.
func MarshalOTLP(serviceName string, traces []Trace) ([]byte, error) {
	var spans []otlpSpan
	for _, tr := range traces {
		for _, s := range tr.Spans {
			os := otlpSpan{
				TraceID:           tr.TraceID,
				SpanID:            s.SpanID,
				ParentSpanID:      s.ParentID,
				Name:              s.Name,
				Kind:              1, // SPAN_KIND_INTERNAL
				StartTimeUnixNano: strconv.FormatInt(s.StartNanos, 10),
				EndTimeUnixNano:   strconv.FormatInt(s.EndNanos, 10),
				Attributes:        otlpAttrs(s.Attrs),
				Status:            otlpStatus{Code: int32(s.Status), Message: s.StatusMsg},
			}
			for _, e := range s.Events {
				os.Events = append(os.Events, otlpEvent{
					TimeUnixNano: strconv.FormatInt(e.TimeNanos, 10),
					Name:         e.Name,
					Attributes:   otlpAttrs(e.Attrs),
				})
			}
			spans = append(spans, os)
		}
	}
	doc := otlpDoc{ResourceSpans: []otlpResourceSpans{{
		Resource: otlpResource{Attributes: otlpAttrs([]Attr{{Key: "service.name", Value: serviceName}})},
		ScopeSpans: []otlpScopeSpans{{
			Scope: otlpScope{Name: "sigil/scenario"},
			Spans: spans,
		}},
	}}}
	return json.MarshalIndent(doc, "", "  ")
}

// --- OTLP/JSON wire shapes (a minimal subset of the proto's JSON form) ---

type otlpDoc struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKV `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId,omitempty"`
	Name              string      `json:"name"`
	Kind              int32       `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []otlpKV    `json:"attributes,omitempty"`
	Events            []otlpEvent `json:"events,omitempty"`
	Status            otlpStatus  `json:"status"`
}

type otlpEvent struct {
	TimeUnixNano string   `json:"timeUnixNano"`
	Name         string   `json:"name"`
	Attributes   []otlpKV `json:"attributes,omitempty"`
}

type otlpStatus struct {
	Code    int32  `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpKV struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

// otlpAnyValue is OTLP's tagged value: exactly one field is set.
type otlpAnyValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"`
	BoolValue   *bool   `json:"boolValue,omitempty"`
}

func otlpAttrs(attrs []Attr) []otlpKV {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]otlpKV, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, otlpKV{Key: a.Key, Value: otlpValue(a.Value)})
	}
	return out
}

func otlpValue(v any) otlpAnyValue {
	switch x := v.(type) {
	case string:
		return otlpAnyValue{StringValue: &x}
	case bool:
		return otlpAnyValue{BoolValue: &x}
	case int:
		s := strconv.FormatInt(int64(x), 10)
		return otlpAnyValue{IntValue: &s}
	case int64:
		s := strconv.FormatInt(x, 10)
		return otlpAnyValue{IntValue: &s}
	default:
		s := ""
		return otlpAnyValue{StringValue: &s}
	}
}
