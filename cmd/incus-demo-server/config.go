package main

type serverConfig struct {
	Server struct {
		Address   string   `yaml:"address"`
		Blocklist []string `yaml:"blocklist"`

		Feedback struct {
			Enabled bool `yaml:"enabled"`
			Timeout int  `yaml:"timeout"`
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
