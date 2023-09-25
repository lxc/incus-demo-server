package main

import (
	"encoding/json"
	"net/http"
)

func restTermsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Generate the response.
	body := make(map[string]interface{})
	body["hash"] = config.Server.termsHash
	body["terms"] = config.Server.Terms

	err := json.NewEncoder(w).Encode(body)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
}
