package main

import (
	"fmt"
	"net"
	"net/http"
	"slices"

	"github.com/lxc/incus/v6/shared/util"
)

func restStatisticsHandler(w http.ResponseWriter, r *http.Request) {
	var err error

	if r.Method != "GET" {
		http.Error(w, "Not implemented", 501)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	// Validate API key.
	requestKey := r.FormValue("key")
	if !slices.Contains(config.Server.Statistics.Keys, requestKey) {
		http.Error(w, "Invalid authentication key", 401)
		return
	}

	// Unique host filtering.
	statsUnique := false
	requestUnique := r.FormValue("unique")
	if util.IsTrue(requestUnique) {
		statsUnique = true
	}

	// Time period filtering.
	requestPeriod := r.FormValue("period")
	if !slices.Contains([]string{"", "total", "current", "hour", "day", "week", "month", "year"}, requestPeriod) {
		http.Error(w, "Invalid period", 400)
		return
	}

	statsPeriod := requestPeriod

	if statsPeriod == "" {
		statsPeriod = "total"
	}

	// Network filtering.
	requestNetwork := r.FormValue("network")
	var statsNetwork *net.IPNet
	if requestNetwork != "" {
		_, statsNetwork, err = net.ParseCIDR(requestNetwork)
		if err != nil {
			http.Error(w, "Invalid network", 400)
			return
		}
	}

	// Query the database.
	count, err := dbGetStats(statsPeriod, statsUnique, statsNetwork)
	if err != nil {
		http.Error(w, "Unable to retrieve statistics", 500)
		return
	}

	// Return to client.
	w.Write([]byte(fmt.Sprintf("%d\n", count)))
}
