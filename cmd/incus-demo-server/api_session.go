package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

func restStartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	if config.Server.Maintenance.Enabled || incusDaemon == nil {
		http.Error(w, "Server in maintenance mode", 500)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Internal server error", 500)
		return
	}
	flusher.Flush()

	statusUpdate := func(msg string) {
		body := make(map[string]interface{})
		body["message"] = msg
		_ = json.NewEncoder(w).Encode(body)
		flusher.Flush()
	}

	requestDate := time.Now().Unix()

	// Extract IP.
	requestIP, _, err := restClientIP(r)
	if err != nil {
		restStartError(w, err, instanceUnknownError)
		return
	}

	// Check Terms of Service.
	requestTerms := r.FormValue("terms")
	if requestTerms == "" {
		http.Error(w, "Missing terms hash", 400)
		return
	}

	if requestTerms != config.Server.termsHash {
		restStartError(w, nil, instanceInvalidTerms)
		return
	}

	// Check for banned users.
	if slices.Contains(config.Server.Blocklist, requestIP) {
		restStartError(w, nil, instanceUserBanned)
		return
	}

	// Count running instances.
	instanceCount, err := dbActiveCount()
	if err != nil {
		instanceCount = config.Server.Limits.Total
	}

	// Server is full.
	if instanceCount >= config.Server.Limits.Total {
		restStartError(w, nil, instanceServerFull)
		return
	}

	// Count instance for requestor IP.
	instanceCount, err = dbActiveCountForIP(requestIP)
	if err != nil {
		instanceCount = config.Server.Limits.IP
	}

	if config.Server.Limits.IP != 0 && instanceCount >= config.Server.Limits.IP {
		restStartError(w, nil, instanceQuotaReached)
		return
	}

	// Create the instance.
	var instanceID int64
	info := map[string]any{}
	instanceExpiry := time.Now().Unix() + int64(config.Session.Expiry)

	id, instanceUUID, instanceName, instanceIP, instanceUsername, instancePassword, err := dbGetAllocated(instanceExpiry, requestDate, requestIP, requestTerms)
	if err == nil {
		// Use a pre-created instance.
		instanceID = id
		info["id"] = instanceUUID
		info["name"] = instanceName
		info["ip"] = instanceIP
		info["username"] = instanceUsername
		info["password"] = instancePassword
		info["expiry"] = instanceExpiry

		// Create a replacement instance.
		go instancePreAllocate()
	} else {
		// Fallback to creating a new one.
		info, err = instanceCreate(false, statusUpdate)
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}

		instanceExpiry = time.Now().Unix() + int64(config.Session.Expiry)
		instanceID, err = dbNew(
			0,
			info["id"].(string),
			info["name"].(string),
			info["ip"].(string),
			info["username"].(string),
			info["password"].(string),
			instanceExpiry, requestDate, requestIP, requestTerms)
		if err != nil {
			incusForceDelete(incusDaemon, info["name"].(string))
			restStartError(w, err, instanceUnknownError)
			return
		}

		info["expiry"] = instanceExpiry
	}

	// Setup cleanup code.
	duration, err := time.ParseDuration(fmt.Sprintf("%ds", config.Session.Expiry))
	if err != nil {
		incusForceDelete(incusDaemon, info["name"].(string))
		restStartError(w, err, instanceUnknownError)
		return
	}

	time.AfterFunc(duration, func() {
		incusForceDelete(incusDaemon, info["name"].(string))
		dbExpire(instanceID)
	})

	err = json.NewEncoder(w).Encode(info)
	if err != nil {
		incusForceDelete(incusDaemon, info["name"].(string))
		restStartError(w, err, instanceUnknownError)
		return
	}

	flusher.Flush()
	return
}

func restInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	if config.Server.Maintenance.Enabled || incusDaemon == nil {
		http.Error(w, "Server in maintenance mode", 500)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Get the id.
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance.
	sessionId, instanceName, instanceIP, instanceUsername, instancePassword, instanceExpiry, err := dbGetInstance(id, false)
	if err != nil || sessionId == -1 {
		http.Error(w, "Session not found", 404)
		return
	}

	body := make(map[string]interface{})

	if !config.Session.ConsoleOnly {
		body["ip"] = instanceIP
		body["username"] = instanceUsername
		body["password"] = instancePassword
		body["fqdn"] = fmt.Sprintf("%s.incus", instanceName)
	}
	body["id"] = id
	body["expiry"] = instanceExpiry

	// Return to the client.
	body["status"] = instanceStarted
	err = json.NewEncoder(w).Encode(body)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		http.Error(w, "Internal server error", 500)
		return
	}
}

func restConsoleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	if config.Server.Maintenance.Enabled || incusDaemon == nil {
		http.Error(w, "Server in maintenance mode", 500)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get the id argument.
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance.
	sessionId, instanceName, _, _, _, _, err := dbGetInstance(id, true)
	if err != nil || sessionId == -1 {
		http.Error(w, "Session not found", 404)
		return
	}

	// Get console width and height.
	width := r.FormValue("width")
	height := r.FormValue("height")

	if width == "" {
		width = "150"
	}

	if height == "" {
		height = "20"
	}

	widthInt, err := strconv.Atoi(width)
	if err != nil {
		http.Error(w, "Invalid width value", 400)
	}

	heightInt, err := strconv.Atoi(height)
	if err != nil {
		http.Error(w, "Invalid width value", 400)
	}

	// Setup websocket with the client.
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	defer conn.Close()

	// Connect to the instance.
	env := make(map[string]string)
	env["USER"] = "root"
	env["HOME"] = "/root"
	env["TERM"] = "xterm"

	inRead, inWrite := io.Pipe()
	outRead, outWrite := io.Pipe()

	// Data handler.
	connWrapper := &wsWrapper{conn: conn}
	go io.Copy(inWrite, connWrapper)
	go io.Copy(connWrapper, outRead)

	// Control socket handler.
	handler := func(conn *websocket.Conn) {
		for {
			_, _, err = conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}

	// Send the exec request.
	req := api.InstanceExecPost{
		Command:     config.Session.Command,
		WaitForWS:   true,
		Interactive: true,
		Environment: env,
		Width:       widthInt,
		Height:      heightInt,
	}

	execArgs := incus.InstanceExecArgs{
		Stdin:    inRead,
		Stdout:   outWrite,
		Stderr:   outWrite,
		Control:  handler,
		DataDone: make(chan bool),
	}

	op, err := incusDaemon.ExecInstance(instanceName, req, &execArgs)
	if err != nil {
		return
	}

	err = op.Wait()
	if err != nil {
		return
	}

	<-execArgs.DataDone

	inWrite.Close()
	outRead.Close()
}
