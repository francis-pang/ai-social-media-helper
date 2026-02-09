package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// containsPathTraversal returns true if the path contains directory traversal
// sequences that could escape the intended directory. (DDR-028 Problem 6)
func containsPathTraversal(p string) bool {
	cleaned := filepath.Clean(p)
	return strings.Contains(cleaned, "..")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}
