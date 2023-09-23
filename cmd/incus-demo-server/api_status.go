package main

import (
	"encoding/json"
	"net/http"
)

const (
	serverOperational statusCode = 0
	serverMaintenance statusCode = 1
)

func restStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var failure bool

	// Parse the remote client information
	address, protocol, err := restClientIP(r)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}

	// Get some instance data
	var instanceCount int
	var instanceNext int

	instanceCount, err = dbActiveCount()
	if err != nil {
		failure = true
	}

	if instanceCount >= config.Server.Limits.Total {
		instanceNext, err = dbNextExpire()
		if err != nil {
			failure = true
		}
	}

	// Generate the response
	body := make(map[string]interface{})
	body["client_address"] = address
	body["client_protocol"] = protocol
	body["feedback"] = config.Server.Feedback.Enabled
	body["session_console_only"] = config.Session.ConsoleOnly
	body["session_network"] = config.Session.Network
	if !config.Server.Maintenance && !failure {
		body["server_status"] = serverOperational
	} else {
		body["server_status"] = serverMaintenance
	}
	body["instance_count"] = instanceCount
	body["instance_max"] = config.Server.Limits.Total
	body["instance_next"] = instanceNext

	err = json.NewEncoder(w).Encode(body)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
}
