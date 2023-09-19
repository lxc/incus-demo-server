package main

type serverConfig struct {
	Server struct {
		Address   string   `yaml:"address"`
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

		Maintenance bool `yaml:"maintenance"`

		Statistics struct {
			Keys []string `yaml:"keys"`
		} `yaml:"stats"`

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
		Command     []string `yaml:"command"`
		Expiry      int      `yaml:"expiry"`
		ConsoleOnly bool     `yaml:"console_only"`
		Network     string   `yaml:"network"`
	} `yaml:"session"`
}
