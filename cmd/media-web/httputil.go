package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// containsPathTraversal returns true if the path contains directory traversal
// sequences that could escape the intended directory. (DDR-028 Problem 6)
//
// We check the raw segments before filepath.Clean resolves them, because
// Clean("/tmp/../etc") silently produces "/etc" with no ".." remaining.
func containsPathTraversal(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}
