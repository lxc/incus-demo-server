package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
	"github.com/pborman/uuid"
)

func restStartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	body := make(map[string]interface{})
	requestDate := time.Now().Unix()

	// Extract IP
	requestIP, _, err := restClientIP(r)
	if err != nil {
		restStartError(w, err, instanceUnknownError)
		return
	}

	// Check Terms of Service
	requestTerms := r.FormValue("terms")
	if requestTerms == "" {
		http.Error(w, "Missing terms hash", 400)
		return
	}

	if requestTerms != config.Server.termsHash {
		restStartError(w, nil, instanceInvalidTerms)
		return
	}

	// Check for banned users
	if shared.StringInSlice(requestIP, config.Server.Blocklist) {
		restStartError(w, nil, instanceUserBanned)
		return
	}

	// Count running instances
	instanceCount, err := dbActiveCount()
	if err != nil {
		instanceCount = config.Server.Limits.Total
	}

	// Server is full
	if instanceCount >= config.Server.Limits.Total {
		restStartError(w, nil, instanceServerFull)
		return
	}

	// Count instance for requestor IP
	instanceCount, err = dbActiveCountForIP(requestIP)
	if err != nil {
		instanceCount = config.Server.Limits.IP
	}

	if config.Server.Limits.IP != 0 && instanceCount >= config.Server.Limits.IP {
		restStartError(w, nil, instanceQuotaReached)
		return
	}

	// Create the instance
	id := uuid.NewRandom().String()
	instanceName := fmt.Sprintf("tryit-%s", id)
	instanceUsername := "admin"
	instancePassword := uuid.NewRandom().String()

	if config.Instance.Source.Instance != "" {
		args := incus.InstanceCopyArgs{
			Name:         instanceName,
			InstanceOnly: true,
		}

		source, _, err := incusDaemon.GetInstance(config.Instance.Source.Instance)
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}

		source.Profiles = config.Instance.Profiles

		// Setup volatile.
		for k := range source.Config {
			if !strings.HasPrefix(k, "volatile.") {
				continue
			}

			delete(source.Config, k)
		}
		source.Config["volatile.apply_template"] = "copy"

		rop, err := incusDaemon.CopyInstance(incusDaemon, *source, &args)
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}

		err = rop.Wait()
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}
	} else {
		req := api.InstancesPost{
			Name: instanceName,
			Source: api.InstanceSource{
				Type:     "image",
				Alias:    config.Instance.Source.Image,
				Server:   "https://images.linuxcontainers.org",
				Protocol: "simplestreams",
			},
			Type: api.InstanceType(config.Instance.Source.InstanceType),
		}
		req.Profiles = config.Instance.Profiles

		rop, err := incusDaemon.CreateInstance(req)
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}

		err = rop.Wait()
		if err != nil {
			restStartError(w, err, instanceUnknownError)
			return
		}
	}

	// Configure the instance devices.
	ct, etag, err := incusDaemon.GetInstance(instanceName)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	if config.Instance.Limits.Disk != "" {
		_, ok := ct.ExpandedDevices["root"]
		if ok {
			ct.Devices["root"] = ct.ExpandedDevices["root"]
			ct.Devices["root"]["size"] = config.Instance.Limits.Disk
		} else {
			ct.Devices["root"] = map[string]string{"type": "disk", "path": "/", "size": config.Instance.Limits.Disk}
		}
	}

	// Configure the instance.
	if api.InstanceType(ct.Type) == api.InstanceTypeContainer {
		ct.Config["security.nesting"] = "true"

		if config.Instance.Limits.Processes > 0 {
			ct.Config["limits.processes"] = fmt.Sprintf("%d", config.Instance.Limits.Processes)
		}
	}

	if config.Instance.Limits.CPU > 0 {
		ct.Config["limits.cpu"] = fmt.Sprintf("%d", config.Instance.Limits.CPU)
	}

	if config.Instance.Limits.Memory != "" {
		ct.Config["limits.memory"] = config.Instance.Limits.Memory
	}

	if !config.Session.ConsoleOnly {
		ct.Config["user.user-data"] = fmt.Sprintf(`#cloud-config
ssh_pwauth: True
manage_etc_hosts: True
users:
 - name: %s
   groups: sudo
   plain_text_passwd: %s
   lock_passwd: False
   shell: /bin/bash
`, instanceUsername, instancePassword)
	}

	op, err := incusDaemon.UpdateInstance(instanceName, ct.Writable(), etag)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	err = op.Wait()
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	// Start the instance
	req := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err = incusDaemon.UpdateInstanceState(instanceName, req, "")
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	err = op.Wait()
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	// Get the IP (30s timeout)
	time.Sleep(2 * time.Second)

	var instanceIP string
	timeout := 30
	for timeout != 0 {
		timeout--
		instState, _, err := incusDaemon.GetInstanceState(instanceName)
		if err != nil {
			incusForceDelete(incusDaemon, instanceName)
			restStartError(w, err, instanceUnknownError)
			return
		}

		for netName, net := range instState.Network {
			if api.InstanceType(ct.Type) == api.InstanceTypeContainer {
				if netName != "eth0" {
					continue
				}
			} else {
				if netName != "enp5s0" {
					continue
				}
			}

			for _, addr := range net.Addresses {
				if addr.Address == "" {
					continue
				}

				if addr.Scope != "global" {
					continue
				}

				if config.Session.Network == "ipv6" && addr.Family != "inet6" {
					continue
				}

				if config.Session.Network == "ipv4" && addr.Family != "inet" {
					continue
				}

				instanceIP = addr.Address
				break
			}

			if instanceIP != "" {
				break
			}
		}

		if instanceIP != "" {
			break
		}

		time.Sleep(1 * time.Second)
	}

	instanceExpiry := time.Now().Unix() + int64(config.Session.Expiry)

	if !config.Session.ConsoleOnly {
		body["ip"] = instanceIP
		body["username"] = instanceUsername
		body["password"] = instancePassword
		body["fqdn"] = fmt.Sprintf("%s.incus", instanceName)
	}
	body["id"] = id
	body["expiry"] = instanceExpiry

	// Setup cleanup code
	duration, err := time.ParseDuration(fmt.Sprintf("%ds", config.Session.Expiry))
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	instanceID, err := dbNew(id, instanceName, instanceIP, instanceUsername, instancePassword, instanceExpiry, requestDate, requestIP, requestTerms)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		restStartError(w, err, instanceUnknownError)
		return
	}

	time.AfterFunc(duration, func() {
		incusForceDelete(incusDaemon, instanceName)
		dbExpire(instanceID)
	})

	// Return to the client
	body["status"] = instanceStarted
	err = json.NewEncoder(w).Encode(body)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		http.Error(w, "Internal server error", 500)
		return
	}
}

func restInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Get the id
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance
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

	// Return to the client
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

	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get the id argument
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance
	sessionId, instanceName, _, _, _, _, err := dbGetInstance(id, true)
	if err != nil || sessionId == -1 {
		http.Error(w, "Session not found", 404)
		return
	}

	// Get console width and height
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

	// Setup websocket with the client
	var upgrader = websocket.Upgrader{
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

	// Connect to the instance
	env := make(map[string]string)
	env["USER"] = "root"
	env["HOME"] = "/root"
	env["TERM"] = "xterm"

	inRead, inWrite := io.Pipe()
	outRead, outWrite := io.Pipe()

	// read handler
	connWrapper := &wsWrapper{conn: conn}
	go io.Copy(inWrite, connWrapper)
	go io.Copy(connWrapper, outRead)

	// control socket handler
	handler := func(conn *websocket.Conn) {
		for {
			_, _, err = conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}

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
		http.Error(w, "Internal server error", 500)
		return
	}

	err = op.Wait()
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}

	<-execArgs.DataDone

	inWrite.Close()
	outRead.Close()
}
