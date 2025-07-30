package main

type serverConfig struct {
	Server struct {
		API struct {
			Address string `yaml:"address"`
		} `yaml:"api"`

		Blocklist []string `yaml:"blocklist"`

		Feedback struct {
			Enabled bool `yaml:"enabled"`
			Timeout int  `yaml:"timeout"`
			Email   struct {
				Server  string `yaml:"server"`
				From    string `yaml:"from"`
				To      string `yaml:"to"`
				Subject string `yaml:"subject"`
			} `yaml:"email"`
		} `yaml:"feedback"`

		Limits struct {
			Total int `yaml:"total"`
			IP    int `yaml:"ip"`
		} `yaml:"limits"`

		Maintenance struct {
			Enabled bool   `yaml:"enabled"`
			Message string `yaml:"message"`
		} `yaml:"maintenance"`

		Proxy struct {
			Address     string `yaml:"address"`
			Certificate string `yaml:"certificate"`
			Key         string `yaml:"key"`
		} `yaml:"proxy"`

		Statistics struct {
			Keys []string `yaml:"keys"`
		} `yaml:"statistics"`

		Terms     string `yaml:"terms"`
		termsHash string
	} `yaml:"server"`

	Incus struct {
		Client struct {
			Certificate string `yaml:"certificate"`
			Key         string `yaml:"key"`
		}

		Project string `yaml:"project"`

		Server struct {
			Certificate string `yaml:"certificate"`
			URL         string `yaml:"url"`
		} `yaml:"server"`

		Target string `yaml:"target"`
	} `yaml:"incus"`

	Instance struct {
		Allocate struct {
			Count  int `yaml:"count"`
			Expiry int `yaml:"expiry"`
		} `yaml:"allocate"`

		Source struct {
			Instance     string `yaml:"instance"`
			Image        string `yaml:"image"`
			InstanceType string `yaml:"type"`
		} `yaml:"source"`

		Profiles []string `yaml:"profiles"`

		Limits struct {
			CPU       int    `yaml:"cpu"`
			Disk      string `yaml:"disk"`
			Processes int    `yaml:"processes"`
			Memory    string `yaml:"memory"`
		} `yaml:"limits"`
	} `yaml:"instance"`

	Session struct {
		Command      []string `yaml:"command"`
		ReadyCommand []string `yaml:"ready_command"`
		Expiry       int      `yaml:"expiry"`
		ConsoleOnly  bool     `yaml:"console_only"`
		Network      string   `yaml:"network"`
	} `yaml:"session"`
}
