package main

// TriageEvent is the input from Step Functions.
type TriageEvent struct {
	Type              string   `json:"type"`
	SessionID         string   `json:"sessionId"`
	JobID             string   `json:"jobId"`
	Model             string   `json:"model,omitempty"`
	ExpectedFileCount int      `json:"expectedFileCount,omitempty"`
	VideoFileNames    []string `json:"videoFileNames,omitempty"`
}

// TriageInitResult is returned by the triage-init-session handler.
type TriageInitResult struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Model     string `json:"model"`
}

// TriageCheckProcessingResult is returned by the triage-check-processing handler.
type TriageCheckProcessingResult struct {
	SessionID      string `json:"sessionId"`
	JobID          string `json:"jobId"`
	Model          string `json:"model"`
	AllProcessed   bool   `json:"allProcessed"`
	ProcessedCount int    `json:"processedCount"`
	ExpectedCount  int    `json:"expectedCount"`
	ErrorCount     int    `json:"errorCount"`
}
