package metrics

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestNew_AutoDimension(t *testing.T) {
	// Set Lambda function name env var
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "TestFunction")
	initOnce.Do(func() {}) // Reset once
	functionName = "TestFunction"

	r := New("TestNamespace")
	if r.namespace != "TestNamespace" {
		t.Errorf("expected namespace TestNamespace, got %s", r.namespace)
	}
	if r.dimensions["FunctionName"] != "TestFunction" {
		t.Errorf("expected FunctionName dimension TestFunction, got %s", r.dimensions["FunctionName"])
	}
}

func TestRecorder_FlushOutput(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	functionName = "" // Clear for test isolation

	rec := New("AiSocialMedia")
	rec.Dimension("Operation", "triage")
	rec.Metric("LatencyMs", 1234.5, UnitMilliseconds)
	rec.Metric("CallCount", 1, UnitCount)
	rec.Property("sessionId", "abc-123")
	rec.Flush()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Parse the JSON output
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		t.Fatalf("failed to parse EMF output as JSON: %v\nOutput: %s", err, output)
	}

	// Check _aws directive exists
	awsDir, ok := doc["_aws"]
	if !ok {
		t.Fatal("missing _aws directive in EMF output")
	}
	awsMap, ok := awsDir.(map[string]interface{})
	if !ok {
		t.Fatal("_aws directive is not a map")
	}

	// Check timestamp exists
	if _, ok := awsMap["Timestamp"]; !ok {
		t.Error("missing Timestamp in _aws directive")
	}

	// Check CloudWatchMetrics
	cwMetrics, ok := awsMap["CloudWatchMetrics"]
	if !ok {
		t.Fatal("missing CloudWatchMetrics in _aws directive")
	}
	cwArr, ok := cwMetrics.([]interface{})
	if !ok || len(cwArr) == 0 {
		t.Fatal("CloudWatchMetrics should be a non-empty array")
	}

	// Check namespace
	cw := cwArr[0].(map[string]interface{})
	if cw["Namespace"] != "AiSocialMedia" {
		t.Errorf("expected namespace AiSocialMedia, got %v", cw["Namespace"])
	}

	// Check dimension value
	if doc["Operation"] != "triage" {
		t.Errorf("expected Operation=triage, got %v", doc["Operation"])
	}

	// Check metric values
	if doc["LatencyMs"] != 1234.5 {
		t.Errorf("expected LatencyMs=1234.5, got %v", doc["LatencyMs"])
	}
	if doc["CallCount"] != float64(1) {
		t.Errorf("expected CallCount=1, got %v", doc["CallCount"])
	}

	// Check property
	if doc["sessionId"] != "abc-123" {
		t.Errorf("expected sessionId=abc-123, got %v", doc["sessionId"])
	}
}

func TestRecorder_FlushEmpty(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	rec := New("Test")
	rec.Flush() // No metrics — should produce no output

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty recorder, got: %s", buf.String())
	}
}

func TestRecorder_Count(t *testing.T) {
	functionName = ""
	rec := New("Test")
	rec.Count("Errors")

	if v, ok := rec.values["Errors"]; !ok || v != float64(1) {
		t.Errorf("expected Errors=1, got %v", v)
	}
	if m, ok := rec.metrics["Errors"]; !ok || m.Unit != UnitCount {
		t.Errorf("expected unit Count, got %v", m.Unit)
	}
}

func TestRecorder_Chaining(t *testing.T) {
	functionName = ""
	rec := New("Test").
		Dimension("Op", "test").
		Metric("Duration", 100, UnitMilliseconds).
		Count("Calls").
		Property("id", "xyz")

	if rec.dimensions["Op"] != "test" {
		t.Error("chaining Dimension failed")
	}
	if rec.values["Duration"] != float64(100) {
		t.Error("chaining Metric failed")
	}
	if rec.values["Calls"] != float64(1) {
		t.Error("chaining Count failed")
	}
	if rec.properties["id"] != "xyz" {
		t.Error("chaining Property failed")
	}
}
