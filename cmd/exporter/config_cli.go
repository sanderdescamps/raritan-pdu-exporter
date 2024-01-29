package main

type CliConfig struct {
	Name       string `short:"n" long:"name" env:"PDU_NAME" description:"Name of the endpoint. Only relevant with multiple endpoints. (default: <name from pdu>)"`
	Address    string `short:"a" long:"address" env:"PDU_ADDRESS" description:"Address of the PDU JSON RPC endpoint"`
	Timeout    int    `long:"timeout" default:"10" description:"Timeout of PDU RPC requests in seconds"`
	Username   string `short:"u" long:"username" env:"PDU_USERNAME" description:"Username for PDU access"`
	Password   string `short:"p" long:"password" env:"PDU_PASSWORD" description:"Password for PDU access"`
	Metrics    bool   `long:"metrics" description:"Enable prometheus metrics endpoint (Deprecated)"`
	Port       uint   `long:"port" default:"2112" description:"Prometheus metrics port"`
	Interval   uint   `short:"i" long:"interval" default:"10" description:"Interval between data scrapes"`
	ConfigPath string `short:"c" long:"config" value-name:"FILE" description:"path to pool config"`
}

func (cliConf *CliConfig) GetConfig() (*Config, error) {
	conf := &Config{
		PduConfig: []PduConfig{},
		ExporterLabels: map[string]bool{
			"use_config_name":   false,
			"serial_number":     true,
			"snmp_sys_contact":  false,
			"snmp_sys_name":     false,
			"snmp_sys_location": false,
		},
	}

	if cliConf.Address != "" && cliConf.Username != "" && cliConf.Password != "" {
		pduConfig := PduConfig{
			Name:     cliConf.Name,
			Address:  cliConf.Address,
			Username: cliConf.Username,
			Password: cliConf.Password,
			Timeout:  cliConf.Timeout,
		}
		conf.PduConfig = append(conf.PduConfig, pduConfig)
	}

	if cliConf.ConfigPath != "" {
		fileConfig, err := ReadConfigFromFile(cliConf.ConfigPath)
		if err != nil {
			return nil, err
		}

		for k, v := range fileConfig.ExporterLabels {
			conf.ExporterLabels[k] = v
		}

		for _, pduConf := range fileConfig.PduConfig {

			if pduConf.Username == "" {
				if fileConfig.Username != "" {
					pduConf.Username = fileConfig.Username
				} else if cliConf.Username != "" {
					pduConf.Username = cliConf.Username
				}
			}

			if pduConf.Password == "" {
				if fileConfig.Password != "" {
					pduConf.Password = fileConfig.Password
				} else if cliConf.Password != "" {
					pduConf.Password = cliConf.Password
				}
			}

			if pduConf.Timeout == 0 {
				if fileConfig.Timeout != 0 {
					pduConf.Timeout = fileConfig.Timeout
				} else if cliConf.Timeout != 0 {
					pduConf.Timeout = cliConf.Timeout
				}
			}

			conf.PduConfig = append(conf.PduConfig, pduConf)
		}
		conf.Metrics = fileConfig.Metrics
		conf.Interval = fileConfig.Interval
		conf.Port = fileConfig.Port
	}

	conf.Metrics = conf.Metrics || cliConf.Metrics
	if conf.Port == 0 && cliConf.Port != 0 {
		conf.Port = cliConf.Port
	}
	if conf.Interval == 0 && cliConf.Interval != 0 {
		conf.Interval = cliConf.Interval
	}

	return conf, nil
}
