package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
	"github.com/pborman/uuid"
)

type statusCode int

const (
	instanceStarted      statusCode = 0
	instanceInvalidTerms statusCode = 1
	instanceServerFull   statusCode = 2
	instanceQuotaReached statusCode = 3
	instanceUserBanned   statusCode = 4
	instanceUnknownError statusCode = 5
)

func instanceCreate(allocate bool, statusUpdate func(string)) (map[string]any, error) {
	info := map[string]any{}

	// Create the instance.
	if statusUpdate != nil {
		statusUpdate("Creating the instance")
	}

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
			return nil, err
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
			return nil, err
		}

		err = rop.Wait()
		if err != nil {
			return nil, err
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
			return nil, err
		}

		err = rop.Wait()
		if err != nil {
			return nil, err
		}
	}

	// Configure the instance devices.
	if statusUpdate != nil {
		statusUpdate("Configuring the instance")
	}

	ct, etag, err := incusDaemon.GetInstance(instanceName)
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		return nil, err
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
		return nil, err
	}

	err = op.Wait()
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		return nil, err
	}

	// Start the instance.
	if statusUpdate != nil {
		statusUpdate("Starting the instance")
	}

	req := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err = incusDaemon.UpdateInstanceState(instanceName, req, "")
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		return nil, err
	}

	err = op.Wait()
	if err != nil {
		incusForceDelete(incusDaemon, instanceName)
		return nil, err
	}

	// Get the IP (30s timeout).
	time.Sleep(2 * time.Second)

	if statusUpdate != nil {
		statusUpdate("Waiting for console")
	}

	var instanceIP string
	timeout := 30
	for timeout != 0 {
		timeout--
		instState, _, err := incusDaemon.GetInstanceState(instanceName)
		if err != nil {
			incusForceDelete(incusDaemon, instanceName)
			return nil, err
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

	// Return to the client.
	info["ip"] = ""
	info["username"] = ""
	info["password"] = ""
	info["fqdn"] = ""
	if !config.Session.ConsoleOnly {
		info["ip"] = instanceIP
		info["username"] = instanceUsername
		info["password"] = instancePassword
		info["fqdn"] = fmt.Sprintf("%s.incus", instanceName)
	}
	info["id"] = id
	info["name"] = instanceName
	info["status"] = instanceStarted

	return info, nil
}

func instancePreAllocate() error {
	// Create the instance.
	info, err := instanceCreate(true, nil)
	if err != nil {
		return err
	}

	// Setup cleanup code.
	duration, err := time.ParseDuration(fmt.Sprintf("%ds", config.Instance.Allocate.Expiry))
	if err != nil {
		incusForceDelete(incusDaemon, info["name"].(string))
		return err
	}

	instanceExpiry := time.Now().Unix() + int64(config.Instance.Allocate.Expiry)
	instanceID, err := dbNew(
		2,
		info["id"].(string),
		info["name"].(string),
		info["ip"].(string),
		info["username"].(string),
		info["password"].(string),
		instanceExpiry,
		0, "", "")
	if err != nil {
		incusForceDelete(incusDaemon, info["name"].(string))
		return err
	}

	time.AfterFunc(duration, func() {
		if dbIsAllocated(instanceID) {
			incusForceDelete(incusDaemon, info["name"].(string))
			dbDelete(instanceID)
			instancePreAllocate()
		}
	})

	return nil
}
