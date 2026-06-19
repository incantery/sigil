package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/trace"
)

// emitTrace serializes the recorded traces to OTLP/JSON and delivers them
// to the configured sinks: a file (--trace) and/or an OTLP/HTTP endpoint
// (--otlp). Both may be set; errors from either are joined.
func emitTrace(rec *traceRecorder, file, otlpURL string) error {
	data, err := trace.MarshalOTLP(rec.serviceName, rec.traces)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	var errs []string
	if file != "" {
		if err := os.WriteFile(file, data, 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("write %s: %v", file, err))
		}
	}
	if otlpURL != "" {
		if err := postOTLP(otlpURL, data); err != nil {
			errs = append(errs, fmt.Sprintf("post %s: %v", otlpURL, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// postOTLP delivers an OTLP/JSON payload to an OTLP/HTTP traces endpoint.
// The URL may be a bare collector base (…:4318) — /v1/traces is appended —
// or the full path. A non-2xx response is an error.
func postOTLP(url string, data []byte) error {
	if !strings.Contains(url, "/v1/traces") {
		url = strings.TrimRight(url, "/") + "/v1/traces"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// traceRecorder turns the e2e event stream into OTLP traces: one trace per
// scenario, a root span for the scenario and a child span per step. It is
// driven by observe(), called for every event in order. Scenarios run one
// at a time (each gets its own browser context), so a single in-flight
// scenario is tracked at a time.
//
// lean drops the per-step/per-bind detail attributes, keeping structure,
// timing, and status — the "prod-lean" verbosity to the rich local default.
type traceRecorder struct {
	serviceName string
	lean        bool
	traces      []trace.Trace
	cur         *scenarioTrace
}

type scenarioTrace struct {
	traceID  string
	rootID   string
	t0       time.Time
	rootNano int64
	name     string
	app      string
	target   string
	spans    []trace.Span
	open     map[int]*openStep
	failed   bool
	failMsg  string
}

type openStep struct {
	spanID    string
	startNano int64
	verb      string
	payload   map[string]any
	line, col int
	events    []trace.Event
}

func newTraceRecorder(serviceName string, lean bool) *traceRecorder {
	return &traceRecorder{serviceName: serviceName, lean: lean}
}

// nanosAt maps an event's elapsed-from-scenario-start milliseconds to an
// absolute wall-clock nanosecond timestamp, anchored at scenario_start.
func (s *scenarioTrace) nanosAt(atMs int64) int64 {
	return s.t0.UnixNano() + atMs*int64(time.Millisecond)
}

func (r *traceRecorder) observe(t ir.Test, e e2eEvent) {
	switch e.Type {
	case "scenario_start":
		name := e.Scenario
		if name == "" {
			name = t.Name
		}
		now := time.Now()
		r.cur = &scenarioTrace{
			traceID:  trace.NewTraceID(),
			rootID:   trace.NewSpanID(),
			t0:       now,
			rootNano: now.UnixNano(),
			name:     name,
			app:      e.App,
			target:   e.Target,
			open:     map[int]*openStep{},
		}

	case "step_start":
		if r.cur == nil {
			return
		}
		r.cur.open[e.Step] = &openStep{
			spanID:    trace.NewSpanID(),
			startNano: r.cur.nanosAt(e.At),
			verb:      e.Verb,
			payload:   e.Payload,
			line:      e.Line,
			col:       e.Col,
		}

	case "bind":
		// A captured binding (extract): annotate the owning step's span.
		if r.cur == nil {
			return
		}
		if os, ok := r.cur.open[e.Step]; ok {
			attrs := []trace.Attr{{Key: "name", Value: e.Name}}
			if !r.lean {
				attrs = append(attrs, trace.Attr{Key: "value", Value: e.Value})
			}
			os.events = append(os.events, trace.Event{
				Name: "bind", TimeNanos: r.cur.nanosAt(e.At), Attrs: attrs,
			})
		}

	case "step_end":
		if r.cur == nil {
			return
		}
		os, ok := r.cur.open[e.Step]
		if !ok {
			// A synthesized step_end (e.g. the navigating step) has no
			// recorded start; give it a zero-width span at this instant.
			os = &openStep{spanID: trace.NewSpanID(), startNano: r.cur.nanosAt(e.At), verb: "step"}
		}
		delete(r.cur.open, e.Step)

		status, msg := trace.StatusOK, ""
		if e.OK == nil || !*e.OK {
			status, msg = trace.StatusError, e.Failure
			r.cur.failed, r.cur.failMsg = true, e.Failure
		}
		r.cur.spans = append(r.cur.spans, trace.Span{
			Name:       spanName(os.verb, e.Step),
			SpanID:     os.spanID,
			ParentID:   r.cur.rootID,
			StartNanos: os.startNano,
			EndNanos:   r.cur.nanosAt(e.At),
			Attrs:      r.stepAttrs(os),
			Status:     status,
			StatusMsg:  msg,
			Events:     os.events,
		})

	case "runner_error":
		if r.cur != nil {
			r.cur.failed, r.cur.failMsg = true, e.Failure
		}

	case "scenario_end":
		if r.cur == nil {
			return
		}
		status, msg := trace.StatusOK, ""
		if e.OK == nil || !*e.OK {
			status = trace.StatusError
			if msg = r.cur.failMsg; msg == "" {
				msg = "scenario failed"
			}
		}
		root := trace.Span{
			Name:       "scenario: " + r.cur.name,
			SpanID:     r.cur.rootID,
			StartNanos: r.cur.rootNano,
			EndNanos:   r.cur.nanosAt(e.At),
			Attrs: []trace.Attr{
				{Key: "sigil.scenario", Value: r.cur.name},
				{Key: "sigil.app", Value: r.cur.app},
				{Key: "sigil.target", Value: r.cur.target},
			},
			Status:    status,
			StatusMsg: msg,
		}
		spans := append([]trace.Span{root}, r.cur.spans...)
		r.traces = append(r.traces, trace.Trace{TraceID: r.cur.traceID, Spans: spans})
		r.cur = nil
	}
}

// stepAttrs builds a span's attributes from the step's verb, source
// position, and (unless lean) its intent payload.
func (r *traceRecorder) stepAttrs(os *openStep) []trace.Attr {
	attrs := []trace.Attr{{Key: "sigil.verb", Value: os.verb}}
	if os.line > 0 {
		attrs = append(attrs,
			trace.Attr{Key: "sigil.line", Value: os.line},
			trace.Attr{Key: "sigil.col", Value: os.col})
	}
	if r.lean {
		return attrs
	}
	for k, v := range os.payload {
		switch v.(type) {
		case string, bool, int, int64:
			attrs = append(attrs, trace.Attr{Key: "sigil." + k, Value: v})
		}
	}
	return attrs
}

func spanName(verb string, step int) string {
	if verb == "" {
		verb = "step"
	}
	return "step " + itoa(step) + ": " + verb
}

// itoa avoids pulling strconv in for a single small int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
