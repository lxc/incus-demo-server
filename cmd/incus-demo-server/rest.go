package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
	"github.com/pborman/uuid"
)

type Feedback struct {
	Rating   int    `json:"rating"`
	Email    string `json:"email"`
	EmailUse int    `json:"email_use"`
	Message  string `json:"message"`
}

func restFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	if !config.Server.Feedback.Enabled {
		http.Error(w, "Feedback reporting is disabled", 400)
		return
	}

	if r.Method == "POST" {
		restFeedbackPostHandler(w, r)
		return
	}

	if r.Method == "GET" {
		restFeedbackGetHandler(w, r)
		return
	}

	if r.Method == "OPTIONS" {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}

		return
	}

	http.Error(w, "Not implemented", 501)
}

func restFeedbackPostHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Get the id argument
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance
	sessionId, _, _, _, _, sessionExpiry, err := dbGetInstance(id, false)
	if err != nil || sessionId == -1 {
		http.Error(w, "Session not found", 404)
		return
	}

	// Check if we can still store feedback
	if time.Now().Unix() > sessionExpiry+int64(config.Server.Feedback.Timeout*60) {
		http.Error(w, "Feedback timeout has been reached", 400)
		return
	}

	// Parse request
	feedback := Feedback{}

	err = json.NewDecoder(r.Body).Decode(&feedback)
	if err != nil {
		http.Error(w, "Invalid JSON data", 400)
		return
	}

	err = dbRecordFeedback(sessionId, feedback)
	if err != nil {
		http.Error(w, "Unable to record feedback data", 500)
		return
	}

	if config.Server.Feedback.Email.Server != "" {
		go emailFeedback(feedback)
	}

	return
}

var emailTpl = template.Must(template.New("emailTpl").Parse(`From: {{ .from }}
To: {{ .to }}
Subject: {{ .subject }}

You received some new user feedback from try-it.

Rating: {{ .rating }} / 5
{{ if .email }}
E-mail: {{ .email }}
{{ end }}
Message:
"""
{{ .message }}
"""
`))

func emailFeedback(feedback Feedback) {
	data := map[string]any{
		"from":    config.Server.Feedback.Email.From,
		"to":      config.Server.Feedback.Email.To,
		"subject": config.Server.Feedback.Email.Subject,
		"rating":  feedback.Rating,
		"email":   "",
		"message": feedback.Message,
	}
	if feedback.EmailUse > 0 {
		data["email"] = feedback.Email
	}

	var sb *strings.Builder = &strings.Builder{}
	err := emailTpl.Execute(sb, data)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return
	}

	err = smtp.SendMail(config.Server.Feedback.Email.Server, nil, config.Server.Feedback.Email.From, []string{config.Server.Feedback.Email.To}, []byte(sb.String()))
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return
	}
}

func restFeedbackGetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Get the id argument
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing session id", 400)
		return
	}

	// Get the instance
	sessionId, _, _, _, _, _, err := dbGetInstance(id, false)
	if err != nil || sessionId == -1 {
		http.Error(w, "Session not found", 404)
		return
	}

	// Get the feedback
	feedbackId, feedbackRating, feedbackEmail, feedbackEmailUse, feedbackComment, err := dbGetFeedback(sessionId)
	if err != nil || feedbackId == -1 {
		http.Error(w, "No existing feedback", 404)
		return
	}

	// Generate the response
	body := make(map[string]interface{})
	body["rating"] = feedbackRating
	body["email"] = feedbackEmail
	body["email_use"] = feedbackEmailUse
	body["feedback"] = feedbackComment

	// Return to the client
	err = json.NewEncoder(w).Encode(body)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}

	return
}

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

func restStatisticsHandler(w http.ResponseWriter, r *http.Request) {
	var err error

	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Validate API key
	requestKey := r.FormValue("key")
	if !shared.StringInSlice(requestKey, config.Server.Statistics.Keys) {
		http.Error(w, "Invalid authentication key", 401)
		return
	}

	// Unique host filtering
	statsUnique := false
	requestUnique := r.FormValue("unique")
	if shared.IsTrue(requestUnique) {
		statsUnique = true
	}

	// Time period filtering
	requestPeriod := r.FormValue("period")
	if !shared.StringInSlice(requestPeriod, []string{"", "total", "current", "hour", "day", "week", "month", "year"}) {
		http.Error(w, "Invalid period", 400)
		return
	}

	statsPeriod := requestPeriod

	if statsPeriod == "" {
		statsPeriod = "total"
	}

	// Network filtering
	requestNetwork := r.FormValue("network")
	var statsNetwork *net.IPNet
	if requestNetwork != "" {
		_, statsNetwork, err = net.ParseCIDR(requestNetwork)
		if err != nil {
			http.Error(w, "Invalid network", 400)
			return
		}
	}

	// Query the database
	count, err := dbGetStats(statsPeriod, statsUnique, statsNetwork)
	if err != nil {
		http.Error(w, "Unable to retrieve statistics", 500)
		return
	}

	// Return to client
	w.Write([]byte(fmt.Sprintf("%d\n", count)))
}

func restTermsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Generate the response
	body := make(map[string]interface{})
	body["hash"] = config.Server.termsHash
	body["terms"] = config.Server.Terms

	err := json.NewEncoder(w).Encode(body)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
}

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

func restStartError(w http.ResponseWriter, err error, code statusCode) {
	body := make(map[string]interface{})
	body["status"] = code

	if err != nil {
		fmt.Printf("error: %s\n", err)
	}

	err = json.NewEncoder(w).Encode(body)
	if err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
}

func restClientIP(r *http.Request) (string, string, error) {
	var address string
	var protocol string

	viaProxy := r.Header.Get("X-Forwarded-For")

	if viaProxy != "" {
		address = viaProxy
	} else {
		host, _, err := net.SplitHostPort(r.RemoteAddr)

		if err == nil {
			address = host
		} else {
			address = r.RemoteAddr
		}
	}

	ip := net.ParseIP(address)
	if ip == nil {
		return "", "", fmt.Errorf("Invalid address: %s", address)
	}

	if ip.To4() == nil {
		protocol = "IPv6"
	} else {
		protocol = "IPv4"
	}

	return address, protocol, nil
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
	connWrapper := &wrapper{conn: conn}
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
