package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"github.com/lxc/incus/v6/client"
	incusTls "github.com/lxc/incus/v6/shared/tls"
	"gopkg.in/yaml.v3"
)

// Global variables.
var incusDaemon incus.InstanceServer
var config serverConfig

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	err := run()
	if err != nil {
		fmt.Printf("error: %s\n", err)
		os.Exit(1)
	}
}

func parseConfig() error {
	data, err := ioutil.ReadFile("config.yaml")
	if os.IsNotExist(err) {
		return fmt.Errorf("The configuration file (config.yaml) doesn't exist.")
	} else if err != nil {
		return fmt.Errorf("Unable to read the configuration: %s", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("Unable to parse the configuration: %s", err)
	}

	if config.Server.API.Address == "" {
		config.Server.API.Address = ":8080"
	}

	if config.Session.Command == nil {
		config.Session.Command = []string{"bash"}
	}

	if config.Instance.Source.InstanceType == "" {
		config.Instance.Source.InstanceType = "container"
	}

	config.Server.Terms = strings.TrimRight(config.Server.Terms, "\n")
	hash := sha256.New()
	io.WriteString(hash, config.Server.Terms)
	config.Server.termsHash = fmt.Sprintf("%x", hash.Sum(nil))

	if config.Instance.Source.Instance == "" && config.Instance.Source.Image == "" {
		return fmt.Errorf("No instance or image specified in configuration")
	}

	if config.Instance.Source.Instance != "" && config.Instance.Source.Image != "" {
		return fmt.Errorf("Only one of instance or image can be specified as the source")
	}

	return nil
}

func run() error {
	// Parse the initial configuration.
	err := parseConfig()
	if err != nil {
		return err
	}

	// Watch for configuration changes.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Unable to setup fsnotify: %s", err)
	}

	err = watcher.Add(".")
	if err != nil {
		return fmt.Errorf("Unable to setup fsnotify watch: %s", err)
	}

	go func() {
		for {
			select {
			case ev := <-watcher.Events:
				if ev.Name != "./config.yaml" {
					continue
				}

				if !ev.Has(fsnotify.Write) {
					continue
				}

				fmt.Printf("Reloading configuration\n")
				err := parseConfig()
				if err != nil {
					fmt.Printf("Failed to parse configuration: %s\n", err)
				}
			case err := <-watcher.Errors:
				fmt.Printf("Inotify error: %s\n", err)
			}
		}
	}()

	// Setup the database.
	err = dbSetup()
	if err != nil {
		return fmt.Errorf("Failed to setup the database: %s", err)
	}

	// Connect to the Incus daemon.
	go func() {
		warning := false
		for {
			if config.Incus.Server.URL == "" {
				incusDaemon, err = incus.ConnectIncusUnix("", nil)
				if err == nil {
					break
				}
			} else {
				// Setup connection arguments.
				args := &incus.ConnectionArgs{
					TLSClientCert: config.Incus.Client.Certificate,
					TLSClientKey:  config.Incus.Client.Key,
					TLSServerCert: config.Incus.Server.Certificate,
				}

				// Connect to the remote server.
				incusDaemon, err = incus.ConnectIncus(config.Incus.Server.URL, args)
				if err == nil {
					break
				}
			}

			if !warning {
				fmt.Printf("Waiting for the Incus server to come online.\n")
				warning = true
			}

			time.Sleep(time.Second)
		}

		if config.Incus.Project != "" {
			incusDaemon = incusDaemon.UseProject(config.Incus.Project)
		}

		if config.Incus.Target != "" {
			incusDaemon = incusDaemon.UseTarget(config.Incus.Target)
		}

		if warning {
			fmt.Printf("Incus is now available.\n")
		}

		// Restore cleanup handler for existing instances.
		instances, err := dbActive()
		if err != nil {
			fmt.Printf("Unable to read current instances: %s", err)
			return
		}

		for _, entry := range instances {
			instanceID := int64(entry[0].(int))
			instanceName := entry[1].(string)
			instanceExpiry := int64(entry[2].(int))

			duration := instanceExpiry - time.Now().Unix()
			timeDuration, err := time.ParseDuration(fmt.Sprintf("%ds", duration))
			if err != nil || duration <= 0 {
				incusForceDelete(incusDaemon, instanceName)
				dbExpire(instanceID)
				continue
			}

			time.AfterFunc(timeDuration, func() {
				incusForceDelete(incusDaemon, instanceName)
				dbExpire(instanceID)
			})
		}

		// Delete former pre-allocated instances.
		instances, err = dbAllocated()
		if err != nil {
			fmt.Printf("Unable to read pre-allocated instances: %s", err)
			return
		}

		for _, entry := range instances {
			instanceID := int64(entry[0].(int))
			instanceName := entry[1].(string)

			incusForceDelete(incusDaemon, instanceName)
			dbDelete(instanceID)
		}

		for i := 0; i < config.Instance.Allocate.Count; i++ {
			err := instancePreAllocate()
			if err != nil {
				fmt.Printf("Failed to pre-allocate instance: %s", err)
				return
			}
		}
	}()

	// Spawn the proxy.
	if config.Server.Proxy.Address != "" {
		if config.Server.Proxy.Certificate == "" && config.Server.Proxy.Key == "" {
			cert, key, err := incusTls.GenerateMemCert(false, false)
			if err != nil {
				return fmt.Errorf("Failed to generate TLS certificate: %w", err)
			}

			config.Server.Proxy.Certificate = string(cert)
			config.Server.Proxy.Key = string(key)
		}

		go proxyListener()
	}

	// Setup the HTTP server.
	r := mux.NewRouter()
	r.Handle("/", http.RedirectHandler("/static", http.StatusMovedPermanently))
	r.PathPrefix("/static").Handler(http.StripPrefix("/static", http.FileServer(http.Dir("static/"))))
	r.HandleFunc("/1.0", restStatusHandler)
	r.HandleFunc("/1.0/console", restConsoleHandler)
	r.HandleFunc("/1.0/feedback", restFeedbackHandler)
	r.HandleFunc("/1.0/info", restInfoHandler)
	r.HandleFunc("/1.0/start", restStartHandler)
	r.HandleFunc("/1.0/statistics", restStatisticsHandler)
	r.HandleFunc("/1.0/terms", restTermsHandler)

	err = http.ListenAndServe(config.Server.API.Address, r)
	if err != nil {
		return err
	}

	return nil
}
