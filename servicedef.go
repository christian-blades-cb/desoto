package main

import (
	"encoding/json"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"regexp"
)

//go:generate ffjson $GOFILE
type ServiceDefinition struct {
	Type          string  `json:"type"`
	ContainerPort int64   `json:"container_port"`
	NamePattern   *string `json:"name_pattern"`
}

// ffjson: skip
type Service struct {
	serviceDef ServiceDefinition
	re         *regexp.Regexp
	key        string
}

func newService(key string, b []byte) (*Service, error) {
	var serviceDef ServiceDefinition
	err := json.Unmarshal(b, serviceDef)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
			"key":   key,
		}).Warn("unable to unmarshal service definition")
	}

	if serviceDef.NamePattern == nil {
		defaultPattern := fmt.Sprintf(`^%s-app-\S+`, key)
		serviceDef.NamePattern = &defaultPattern
	}

	svc := Service{
		serviceDef: serviceDef,
		key:        key,
	}

	compiledRegexp, err := regexp.Compile(*serviceDef.NamePattern)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err,
			"key":     key,
			"pattern": fmt.Sprint(b),
		}).Warn("unable to compile regexp expression")
		return &svc, err
	}
	svc.re = compiledRegexp

	return &svc, nil
}

type services []*Service
