package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"text/template"
	"time"
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
{{- if .email }}
E-mail: {{ .email }}
{{ end }}
{{- if .message }}
Message:
"""
{{ .message }}
"""
{{ end }}
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
