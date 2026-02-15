package main

// DescriptionEvent is the input from the API Lambda.
type DescriptionEvent struct {
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	JobID       string   `json:"jobId"`
	Keys        []string `json:"keys,omitempty"`
	GroupLabel  string   `json:"groupLabel,omitempty"`
	TripContext string   `json:"tripContext,omitempty"`
	Feedback    string   `json:"feedback,omitempty"`
}
