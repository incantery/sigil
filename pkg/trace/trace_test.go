package trace

import (
	"encoding/json"
	"testing"
)

func TestMarshalOTLPShape(t *testing.T) {
	traces := []Trace{{
		TraceID: "0123456789abcdef0123456789abcdef",
		Spans: []Span{
			{
				Name: "scenario: demo", SpanID: "1111111111111111",
				StartNanos: 1000, EndNanos: 5000,
				Attrs:  []Attr{{Key: "sigil.app", Value: "Demo"}},
				Status: StatusOK,
			},
			{
				Name: "step 1: extract", SpanID: "2222222222222222", ParentID: "1111111111111111",
				StartNanos: 1000, EndNanos: 1200,
				Attrs:  []Attr{{Key: "sigil.line", Value: 7}},
				Status: StatusError, StatusMsg: "boom",
				Events: []Event{{Name: "bind", TimeNanos: 1100, Attrs: []Attr{{Key: "name", Value: "acct"}}}},
			},
		},
	}}
	data, err := MarshalOTLP("sigil-test", traces)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Round-trip into a generic map and assert the OTLP nesting + encodings.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rs := doc["resourceSpans"].([]any)[0].(map[string]any)
	spans := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}

	root := spans[0].(map[string]any)
	if root["traceId"] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("traceId not hex-preserved: %v", root["traceId"])
	}
	if _, hasParent := root["parentSpanId"]; hasParent {
		t.Fatalf("root span should omit parentSpanId")
	}
	// Timestamps are string-encoded uint64 nanos in OTLP/JSON.
	if root["startTimeUnixNano"] != "1000" {
		t.Fatalf("start nanos not string-encoded: %v", root["startTimeUnixNano"])
	}

	step := spans[1].(map[string]any)
	if step["parentSpanId"] != "1111111111111111" {
		t.Fatalf("step parent wrong: %v", step["parentSpanId"])
	}
	if code := step["status"].(map[string]any)["code"]; code != float64(StatusError) {
		t.Fatalf("status code wrong: %v", code)
	}
	// Int attribute encodes as a stringValue under intValue.
	attr := step["attributes"].([]any)[0].(map[string]any)
	if iv := attr["value"].(map[string]any)["intValue"]; iv != "7" {
		t.Fatalf("int attr not string-encoded intValue: %v", iv)
	}
	ev := step["events"].([]any)[0].(map[string]any)
	if ev["name"] != "bind" {
		t.Fatalf("span event missing: %v", ev)
	}
}

func TestIDWidths(t *testing.T) {
	if len(NewTraceID()) != 32 {
		t.Fatalf("trace id must be 32 hex chars")
	}
	if len(NewSpanID()) != 16 {
		t.Fatalf("span id must be 16 hex chars")
	}
}
