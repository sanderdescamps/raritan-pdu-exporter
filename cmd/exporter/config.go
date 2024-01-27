package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/jessevdk/go-flags"
	"k8s.io/klog/v2"
)

type Config struct {
	Metrics        bool            `json:"metrics" yaml:"metrics"`
	Port           uint            `json:"port" yaml:"port"`
	Interval       uint            `json:"interval" yaml:"interval"`
	ExporterLabels map[string]bool `json:"exporter_labels" yaml:"exporter_labels"`
	PduConfig      []PduConfig     `json:"pdu_config" yaml:"pdu_config"`
}

type PduConfig struct {
	Name         string            `json:"name" yaml:"name"`
	Address      string            `json:"address" yaml:"address"`
	Timeout      int               `json:"timeout" yaml:"timeout"`
	Username     string            `json:"username" yaml:"username"`
	Password     string            `json:"password" yaml:"password"`
	StaticLabels map[string]string `json:"static_labels" yaml:"static_labels"`
}

func (cc *PduConfig) Url() string {
	if cc.Address == "" {
		return ""
	} else if !strings.HasPrefix(cc.Address, "https://") && !strings.HasPrefix(cc.Address, "http://") {
		if strings.Contains(cc.Address, "443") {
			return "https://" + cc.Address
		} else {
			return "http://" + cc.Address
		}
	}
	return cc.Address
}

func LoadConfig(args []string) (*Config, error) {
	klogFs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(klogFs)
	cliConf := &CliConfig{}

	p := flags.NewParser(cliConf, flags.Default|flags.IgnoreUnknown)

	fs, err := p.ParseArgs(args)
	if err != nil {
		if _, ok := err.(*flags.Error); !ok {
			return nil, fmt.Errorf("error parsing args: %v", err)
		}
		return nil, err
	}

	klogFs.Parse(fs)

	conf, err := cliConf.GetConfig()
	if err != nil {
		return nil, err
	}
	klog.Infof("Server config: port=%d metrics=%t\n", conf.Port, conf.Metrics)
	for _, p := range conf.PduConfig {
		var name string
		if p.Name != "" {
			name = p.Name
		} else {
			name = "<no name defined>"
		}
		klog.Infof("PDU config: name=%s url=%s\n", name, p.Url())
	}

	return conf, nil
}
