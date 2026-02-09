// Package metrics provides a lightweight AWS CloudWatch Embedded Metrics Format (EMF)
// utility for emitting custom metrics from Lambda functions. EMF metrics are written
// as structured JSON to stdout, where CloudWatch automatically extracts them — no API
// calls, no added latency, no extra cost beyond log ingestion.
//
// See: https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch_Embedded_Metric_Format_Specification.html
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Standard CloudWatch metric units.
const (
	UnitMilliseconds = "Milliseconds"
	UnitCount        = "Count"
	UnitBytes        = "Bytes"
	UnitNone         = "None"
)

// metricDef holds the name and unit for a single metric.
type metricDef struct {
	Name string `json:"Name"`
	Unit string `json:"Unit"`
}

// emfDirective is the _aws metadata block required by EMF.
type emfDirective struct {
	Timestamp         int64      `json:"Timestamp"`
	CloudWatchMetrics []cwMetric `json:"CloudWatchMetrics"`
}

// cwMetric defines a CloudWatch metric namespace, dimensions, and metric definitions.
type cwMetric struct {
	Namespace  string      `json:"Namespace"`
	Dimensions [][]string  `json:"Dimensions"`
	Metrics    []metricDef `json:"Metrics"`
}

// Recorder accumulates dimensions, metrics, and properties for a single EMF flush.
// It is NOT safe for concurrent use from multiple goroutines; create one per operation.
type Recorder struct {
	namespace  string
	dimensions map[string]string
	metrics    map[string]metricDef
	values     map[string]interface{}
	properties map[string]interface{}
}

var (
	// functionName is cached from AWS_LAMBDA_FUNCTION_NAME at init time.
	functionName string
	initOnce     sync.Once
)

func initFunctionName() {
	functionName = os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
}

// New creates a new EMF Recorder with the given CloudWatch namespace.
// It automatically adds the FunctionName dimension from the Lambda environment.
func New(namespace string) *Recorder {
	initOnce.Do(initFunctionName)
	r := &Recorder{
		namespace:  namespace,
		dimensions: make(map[string]string),
		metrics:    make(map[string]metricDef),
		values:     make(map[string]interface{}),
		properties: make(map[string]interface{}),
	}
	if functionName != "" {
		r.dimensions["FunctionName"] = functionName
	}
	return r
}

// Dimension adds a dimension key-value pair. Dimensions are indexed in CloudWatch
// and appear as filterable attributes on the metric.
func (r *Recorder) Dimension(key, value string) *Recorder {
	r.dimensions[key] = value
	return r
}

// Metric records a named metric value with a CloudWatch unit.
// Use the Unit* constants (UnitMilliseconds, UnitCount, UnitBytes, UnitNone).
func (r *Recorder) Metric(name string, value float64, unit string) *Recorder {
	r.metrics[name] = metricDef{Name: name, Unit: unit}
	r.values[name] = value
	return r
}

// Count is a convenience for recording a count metric (value = 1).
func (r *Recorder) Count(name string) *Recorder {
	return r.Metric(name, 1, UnitCount)
}

// Property adds a non-metric field to the EMF document. Properties are searchable
// in CloudWatch Logs Insights but do not create CloudWatch metrics (no cost).
func (r *Recorder) Property(key string, value interface{}) *Recorder {
	r.properties[key] = value
	return r
}

// Flush serializes the EMF document as a single JSON line to stdout.
// CloudWatch Logs automatically extracts the embedded metrics.
// After flushing, the Recorder should not be reused.
func (r *Recorder) Flush() {
	if len(r.metrics) == 0 {
		return // Nothing to emit
	}

	// Build the top-level JSON object
	doc := make(map[string]interface{})

	// Build metric definitions list
	metricDefs := make([]metricDef, 0, len(r.metrics))
	for _, m := range r.metrics {
		metricDefs = append(metricDefs, m)
	}

	// Build dimension keys list
	dimKeys := make([]string, 0, len(r.dimensions))
	for k := range r.dimensions {
		dimKeys = append(dimKeys, k)
	}

	// _aws directive
	doc["_aws"] = emfDirective{
		Timestamp: time.Now().UnixMilli(),
		CloudWatchMetrics: []cwMetric{{
			Namespace:  r.namespace,
			Dimensions: [][]string{dimKeys},
			Metrics:    metricDefs,
		}},
	}

	// Add dimension values as top-level fields
	for k, v := range r.dimensions {
		doc[k] = v
	}

	// Add metric values as top-level fields
	for k, v := range r.values {
		doc[k] = v
	}

	// Add properties as top-level fields
	for k, v := range r.properties {
		doc[k] = v
	}

	data, err := json.Marshal(doc)
	if err != nil {
		// Best-effort: log to stderr if marshaling fails
		fmt.Fprintf(os.Stderr, "emf: failed to marshal metrics: %v\n", err)
		return
	}

	// EMF must be a single line on stdout
	fmt.Fprintln(os.Stdout, string(data))
}
