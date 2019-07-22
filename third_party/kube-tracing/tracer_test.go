package kubetracing

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opentracing/opentracing-go"
)

const (
	containerName = "jaeger-tce-debug"
	// Run scripts/build-jaeger-image.sh in code.byted.org/tce/jaeger:v1.11.0-tce-kube-tracing to
	// update the image.
	dockerImageUrl = "hub.byted.org/jaeger-tce-debug:e74bd9c125ca0a9bc37e5c297c355e8d"
)

var errJaegerContainerRuning = errors.New("jaeger debug container is already running")

type fakeContextBuilder struct {
}

func (cb *fakeContextBuilder) buildContext(ctxType ContextType) interface{} {
	switch ctxType {
	case ContextHTTPRequest:
		ctx := &http.Request{Header: make(http.Header)}
		ctx.Header.Add("k", "v")
		return ctx
	case ContextKVString:
		return "k1:v1,k2:v2"
	}
	return nil
}

// setupLocalJaegerCluster run an all-in-one jaeger instance in docker container if there
// isn't a running one.
func setupLocalJaegerCluster() error {
	// Check if there is a running jaeger container.
	checkCmd := fmt.Sprintf("docker ps --filter name=%s | grep %s | wc -l", containerName, containerName)
	out, err := exec.Command("bash", "-c", checkCmd).Output()
	if err != nil {
		return err
	}
	if i, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); i != 0 {
		return errJaegerContainerRuning
	}
	// Run a new one if there isn't.
	startCmd := fmt.Sprintf("docker run -d --rm --net host --name %s %s", containerName, dockerImageUrl)
	err = exec.Command("bash", "-c", startCmd).Run()
	time.Sleep(10 * time.Second)
	return err
}

// waitFlush wait for reporter of tracer to flush pending spans.
//
// defaultBufferFlushInterval of remoteReporter is 1 second, so wait at
// least 3 second.
func waitFlush() {
	time.Sleep(3 * time.Second)
}

func TestTraceFinishedSpan(t *testing.T) {
	opentracing.UnregisterGlobalTracer()

	os.Setenv("JAEGER_SERVICE_NAME", "FinishedSpan")
	os.Setenv("JAEGER_AGENT_HOST", "127.0.0.1")
	os.Setenv("JAEGER_AGENT_PORT", "6831")
	os.Setenv("JAEGER_REPORTER_PERIODIC_FLUSH", "false")
	os.Setenv("KUBE_TRACING_DEBUG_LOG_ENABLED", "true")
	if err := setupLocalJaegerCluster(); err != errJaegerContainerRuning && err != nil {
		t.Errorf("failed when setup local jaeger cluster, %v", err)
	}

	span := Trace(nil, TraceStart, nil, "FinishedSpan1")
	span = Trace(span, TraceBaggage, nil, "", "baggageItem1", "value1")
	span = Trace(span, TraceTag, nil, "", "key1", "value1", "key2", "value2")
	time.Sleep(1 * time.Second)
	span = Trace(span, TraceLog, nil, "", "info", "This is an info log")
	time.Sleep(1 * time.Second)
	span = Trace(span, TraceLog, nil, "", "warn", "This is an warn log")

	// Child spans...
	childSpan1 := Trace(nil, TraceStart, span, "FinishedSpan1.ChildSpan1", ChildOfRef)
	time.Sleep(1 * time.Second)
	Trace(childSpan1, TraceFinish, nil, "FinishedSpan1.ChildSpan1")

	time.Sleep(1 * time.Second)
	span = Trace(span, TraceLog, nil, "", "error", "This is an error log")
	Trace(span, TraceFinish, nil, "FinishedSpan1")
	waitFlush()
}

func TestTraceUnfinishedSpan(t *testing.T) {
	opentracing.UnregisterGlobalTracer()

	os.Setenv("JAEGER_SERVICE_NAME", "UnfinishedSpan")
	os.Setenv("JAEGER_AGENT_HOST", "127.0.0.1")
	os.Setenv("JAEGER_AGENT_PORT", "6831")
	os.Setenv("JAEGER_REPORTER_PERIODIC_FLUSH", "true")
	os.Setenv("KUBE_TRACING_DEBUG_LOG_ENABLED", "true")
	if err := setupLocalJaegerCluster(); err != errJaegerContainerRuning && err != nil {
		t.Errorf("failed when setup local jaeger cluster, %v", err)
	}

	ctxBuilder := fakeContextBuilder{}
	ctx := ctxBuilder.buildContext(ContextHTTPRequest)
	span := Trace(nil, TraceStart, ctx, "UnfinishedSpan1", ChildOfRef)
	// During sleep, we can still query the unfinished span.
	time.Sleep(30 * time.Second)

	// Child spans...
	childSpan1 := Trace(nil, TraceStart, span.Context(), "FinishedSpan1.ChildSpan1", ChildOfRef)
	time.Sleep(30 * time.Second)
	Trace(childSpan1, TraceFinish, nil, "FinishedSpan1.ChildSpan1")

	time.Sleep(120 * time.Second)
	Trace(span, TraceFinish, nil, "UnfinishedSpan1")
	waitFlush()
}

func TestTraceIndirectPropagation(t *testing.T) {
	opentracing.UnregisterGlobalTracer()

	os.Setenv("JAEGER_SERVICE_NAME", "IndirectPropagationSpan")
	os.Setenv("JAEGER_AGENT_HOST", "127.0.0.1")
	os.Setenv("JAEGER_AGENT_PORT", "6831")
	os.Setenv("JAEGER_REPORTER_PERIODIC_FLUSH", "false")
	os.Setenv("KUBE_TRACING_DEBUG_LOG_ENABLED", "true")
	if err := setupLocalJaegerCluster(); err != errJaegerContainerRuning && err != nil {
		t.Errorf("failed when setup local jaeger cluster, %v", err)
	}

	ctxBuilder := fakeContextBuilder{}
	ctx := ctxBuilder.buildContext(ContextHTTPRequest)
	span := Trace(nil, TraceStart, ctx, "IndirectPropagationSpan1", "IndirectPropagationSpan1")
	time.Sleep(1 * time.Second)

	// Child spans...
	childSpan1 := Trace(nil, TraceStart, "IndirectPropagationSpan1", "IndirectPropagationSpan1.ChildSpan1")
	time.Sleep(1 * time.Second)
	Trace(childSpan1, TraceFinish, nil, "IndirectPropagationSpan1.ChildSpan1")

	time.Sleep(1 * time.Second)
	Trace(span, TraceFinish, nil, "IndirectPropagationSpan1")
	waitFlush()
}
