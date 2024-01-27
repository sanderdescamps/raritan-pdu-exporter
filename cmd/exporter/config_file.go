package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileConfig struct {
	// Address   string      `json:"address" yaml:"address"`
	Timeout        int             `json:"timeout" yaml:"timeout"`
	Username       string          `json:"username" yaml:"username"`
	Password       string          `json:"password" yaml:"password"`
	Metrics        bool            `json:"metrics" yaml:"metrics"`
	Port           uint            `json:"port" yaml:"port"`
	Interval       uint            `json:"interval" yaml:"interval"`
	ExporterLabels map[string]bool `json:"exporter_labels" yaml:"exporter_labels"`
	PduConfig      []PduConfig     `json:"pdu_config" yaml:"pdu_config"`
}

func ReadConfigFromFile(confPath string) (*FileConfig, error) {
	fc := &FileConfig{}

	content, err := os.ReadFile(confPath)
	if err != nil {
		log.Printf("[error] %v\n", err)
	}
	if strings.HasSuffix(confPath, ".yaml") || strings.HasSuffix(confPath, ".yml") {
		err := yaml.Unmarshal(content, fc)
		if err != nil {
			log.Printf("[error] %v\n", err)
		}
	} else if strings.HasSuffix(confPath, ".json") {
		err := json.Unmarshal(content, fc)
		if err != nil {
			log.Printf("[error] %v\n", err)
		}
	}

	return fc, nil
}
