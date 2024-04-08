package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

func incusForceDelete(d incus.InstanceServer, name string) error {
	muCreate.RLock()
	defer muCreate.RUnlock()

	req := api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   true,
	}

	op, err := d.UpdateInstanceState(name, req, "")
	if err == nil {
		op.Wait()
	}

	op, err = d.DeleteInstance(name)
	if err != nil {
		return err
	}

	return op.Wait()
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
