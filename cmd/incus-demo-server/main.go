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
	"github.com/lxc/incus/client"
	"gopkg.in/yaml.v3"
)

// Global variables
var incusDaemon incus.InstanceServer
var config serverConfig

type statusCode int

const (
	serverOperational statusCode = 0
	serverMaintenance statusCode = 1

	instanceStarted      statusCode = 0
	instanceInvalidTerms statusCode = 1
	instanceServerFull   statusCode = 2
	instanceQuotaReached statusCode = 3
	instanceUserBanned   statusCode = 4
	instanceUnknownError statusCode = 5
)

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

	if config.Server.Address == "" {
		config.Server.Address = ":8080"
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
	var err error

	// Setup configuration
	err = parseConfig()
	if err != nil {
		return err
	}

	// Watch for configuration changes
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

	// Connect to the Incus daemon
	warning := false
	for {
		incusDaemon, err = incus.ConnectIncusUnix("", nil)
		if err == nil {
			break
		}

		if !warning {
			fmt.Printf("Waiting for the Incus server to come online.\n")
			warning = true
		}
		time.Sleep(time.Second)
	}

	if warning {
		fmt.Printf("Incus is now available. Daemon starting.\n")
	}

	// Setup the database
	err = dbSetup()
	if err != nil {
		return fmt.Errorf("Failed to setup the database: %s", err)
	}

	// Restore cleanup handler for existing instances
	instances, err := dbActive()
	if err != nil {
		return fmt.Errorf("Unable to read current instances: %s", err)
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

	// Setup the HTTP server
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

	err = http.ListenAndServe(config.Server.Address, r)
	if err != nil {
		return err
	}

	return nil
}