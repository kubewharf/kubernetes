package kubetracing

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go"
	"github.com/uber/jaeger-client-go/config"
)

type TraceAction string

const (
	TraceStart   TraceAction = "Start"
	TraceFinish  TraceAction = "Finish"
	TraceTag     TraceAction = "SetTag"
	TraceLog     TraceAction = "SetLog"
	TraceBaggage TraceAction = "SetBaggageItem"
	TraceGetSpan TraceAction = "GetSpan"
	TraceSetSpan TraceAction = "SetSpan"
	TraceDelSpan TraceAction = "DelSpan"
)

type SpanReferenceType opentracing.SpanReferenceType

const (
	ChildOfRef     SpanReferenceType = SpanReferenceType(opentracing.ChildOfRef)
	FollowsFromRef SpanReferenceType = SpanReferenceType(opentracing.FollowsFromRef)
)

type ContextType string

const (
	ContextHTTPRequest ContextType = "HTTPRequest"
	ContextKVString    ContextType = "KVString"
)

var enablePeriodicReport = false
var regexValidSpanContext *regexp.Regexp

const defaultFlushInterval = 5 * time.Second

// setGlobalTracer set the global tracer for this process.
func setGlobalTracer() {
	if opentracing.IsGlobalTracerRegistered() {
		return
	}
	if len(os.Getenv("JAEGER_SERVICE_NAME")) == 0 {
		os.Setenv("JAEGER_SERVICE_NAME", os.Args[0][strings.LastIndex(os.Args[0], "/")+1:])
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return
	}
	cfg.Sampler.Type = "const"
	cfg.Sampler.Param = 1
	// TODO: A quick hack to ensure random generators get different seeds,
	// which are based on current time.
	time.Sleep(100 * time.Millisecond)
	tracer, _, err := cfg.NewTracer()
	if err != nil {
		return
	}
	if os.Getenv("JAEGER_REPORTER_PERIODIC_FLUSH") == "true" {
		enablePeriodicReport = true
	}
	if os.Getenv("KUBE_TRACING_DEBUG_LOG_ENABLED") == "true" {
		enableDebugLog = true
	}
	opentracing.SetGlobalTracer(tracer)

	regexValidSpanContext, _ = regexp.Compile("[a-z0-9]+\\:[a-z0-9]+\\:[a-z0-9]+\\:[a-z0-9]+")
}

const DeprecatedChildSpan = "DeprecatedChildSpan"

func ContextStringFromSpan(span opentracing.Span) string {
	if span != nil {
		if jaegerSpan, ok := span.(*jaeger.Span); ok {
			return jaegerSpan.Context().(jaeger.SpanContext).String()
		}
	}
	return DeprecatedChildSpan
}

// traceStart creates and starts a new span from given context.
func traceStart(context interface{}, operationName string, opts ...interface{}) opentracing.Span {
	var (
		tracer      opentracing.Tracer = opentracing.GlobalTracer()
		spanContext opentracing.SpanContext
	)
	switch context.(type) {
	case *http.Request:
		spanContext, _ = tracer.Extract(opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(context.(*http.Request).Header))
	case opentracing.Span:
		spanContext = context.(opentracing.Span).Context()
	case opentracing.SpanContext:
		spanContext = context.(opentracing.SpanContext)
	case string:
		if parentSpan := globalSpanStore.get(context.(string)); parentSpan != nil {
			spanContext = parentSpan.Context()
			debugLog("[kube-tracing] traceStart, infer context indirectly from parent span by gloabl key %s", context.(string))
		} else {
			// Only extract SpanContext from valid context string.
			if regexValidSpanContext.MatchString(context.(string)) {
				spanContext, _ = jaeger.ContextFromString(context.(string))
			}
		}
	}
	// If context is provided but no SpanContext is inferred, no new span should be generated.
	if context != nil && spanContext == nil {
		return nil
	}
	refType := opentracing.ChildOfRef
	spanRef := opentracing.ChildOf(spanContext)
	// The first opt should be a SpanReference if there is one.
	if len(opts) > 0 {
		if r, ok := opts[0].(SpanReferenceType); ok && (r == 0 || r == 1) {
			refType = opentracing.SpanReferenceType(r)
			switch refType {
			case opentracing.ChildOfRef:
				spanRef = opentracing.ChildOf(spanContext)
			case opentracing.FollowsFromRef:
				spanRef = opentracing.FollowsFrom(spanContext)
			}
			opts = opts[1:]
		}
	}
	startOpts := []opentracing.StartSpanOption{}
	startOpts = append(startOpts, spanRef)
	startOpts = append(startOpts, opentracing.Tag{KubetracingSpanFinishedTag, false})
	if enablePeriodicReport {
		startOpts = append(startOpts, opentracing.FlushInterval(defaultFlushInterval))
	}
	span := tracer.StartSpan(operationName, startOpts...)

	// Baggage items from parent span should be set as tags in child.
	if spanContext != nil {
		spanContext.ForeachBaggageItem(func(k, v string) bool {
			if k != KubetracingSpanKey {
				traceKV(span, TraceTag, k, v)
			}
			return true
		})
	}
	// On some circumstances, SpanContext cannot easily propagate by directly function call, then
	// it's necessary to store span with a global uniq key and to be easily retrieved later.
	//
	// NOTE: A key should be consistent during full path of a trace, e.g. a docker container id.
	if len(opts) > 0 {
		if key, ok := opts[0].(string); ok {
			traceStore(span, TraceSetSpan, key)
		}
	}
	return span
}

// traceFinish finishs a pending span.
func traceFinish(span opentracing.Span) opentracing.Span {
	if span != nil {
		if key := span.BaggageItem(KubetracingSpanKey); key != "" {
			traceStore(span, TraceDelSpan, key)
		}
		span = traceKV(span, TraceTag, KubetracingSpanFinishedTag, true)
		span.Finish()
	}
	return nil
}

// traceKV is a multiplexor for a range of kv-style logging behaviors on span.
func traceKV(span opentracing.Span, action TraceAction, kv ...interface{}) opentracing.Span {
	if span == nil || len(kv) == 0 {
		return span
	}
	switch action {
	case TraceTag:
		// traceTag sets tags or baggage items on span.
		for i := 0; i < len(kv); i = i + 2 {
			if _, ok := kv[i].(string); ok {
				span.SetTag(kv[i].(string), kv[i+1])
			}
		}

	case TraceLog:
		// NOTE: First argument of logs should be log level.
		logLevel, ok := kv[0].(string)
		if !ok {
			return span
		}
		kv := kv[1:]
		for _, log := range kv {
			logStr, ok := log.(string)
			if !ok {
				logStr = fmt.Sprintf("%v", log)
			}
			switch logLevel {
			case "info":
				Info(span, logStr)
			case "warn":
				Warn(span, logStr)
			case "error":
				Error(span, logStr)
			case "fatal":
				Fatal(span, logStr)
			}
		}

	case TraceBaggage:
		// NOTE: A baggage item is also a tag in kube-tracing context. Some value, e.g. a docker container id,
		// should be set as a baggage item to pass through a entire control flow.
		for i := 0; i < len(kv); i = i + 2 {
			if _, ok := kv[i].(string); ok {
				span.SetBaggageItem(kv[i].(string), kv[i+1].(string))
				span.SetTag(kv[i].(string), kv[i+1])
			}
		}
	}
	return span
}

// traceStore performs crd operations of a span in the global store.
func traceStore(span opentracing.Span, action TraceAction, keys ...interface{}) opentracing.Span {
	if len(keys) == 0 {
		return span
	}
	var key string
	var ok bool
	if key, ok = keys[0].(string); !ok {
		return span
	}
	switch action {
	case TraceGetSpan:
		return globalSpanStore.get(key)
	case TraceSetSpan:
		if span != nil {
			span.SetBaggageItem(KubetracingSpanKey, key)
		}
		globalSpanStore.set(key, span)
		debugLog("[kube-tracing] traceStore, save new span to gloabl store with key %s", key)
	case TraceDelSpan:
		globalSpanStore.del(key)
		debugLog("[kube-tracing] traceStore, delete span from gloabl store with key %s", key)
		return nil
	}
	return span
}

// Trace is a multiplexor for a range of different operations on kube-tracing client.
// It's designed to be easily use and do all the things of completely tracing behaviors
// to decoupling from instrumented code.
func Trace(span opentracing.Span, spanAction TraceAction, context interface{}, operationName string, opts ...interface{}) opentracing.Span {
	if !opentracing.IsGlobalTracerRegistered() {
		setGlobalTracer()
	}
	switch spanAction {
	case TraceStart:
		span = traceStart(context, operationName, opts...)
	case TraceFinish:
		span = traceFinish(span)
	case TraceTag:
		span = traceKV(span, TraceTag, opts...)
	case TraceBaggage:
		span = traceKV(span, TraceBaggage, opts...)
	case TraceLog:
		span = traceKV(span, TraceLog, opts...)
	case TraceGetSpan, TraceSetSpan, TraceDelSpan:
		span = traceStore(span, spanAction, opts...)
	}
	return span
}
