package main

import (
	"encoding/json"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"regexp"
	"strings"
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

func leafFromKey(key string) string {
	lastSlash := strings.LastIndex(key, "/")
	if lastSlash+1 >= len(key) { // also includes empty strings
		log.WithField("key", key).Fatal("specified key is not a leaf node")
	}

	if lastSlash == -1 {
		return key
	}

	return key[lastSlash+1:]
}

func newService(key string, b []byte) (*Service, error) {
	var serviceDef ServiceDefinition
	err := json.Unmarshal(b, &serviceDef)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
			"key":   key,
		}).Warn("unable to unmarshal service definition")
	}

	cleanKey := leafFromKey(key)

	if serviceDef.NamePattern == nil {
		defaultPattern := fmt.Sprintf(`^%s-app-\S+`, cleanKey)
		serviceDef.NamePattern = &defaultPattern
	}

	svc := Service{
		serviceDef: serviceDef,
		key:        cleanKey,
	}

	compiledRegexp, err := regexp.Compile(*serviceDef.NamePattern)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err,
			"key":     cleanKey,
			"pattern": fmt.Sprint(b),
		}).Warn("unable to compile regexp expression")
		return &svc, err
	}
	svc.re = compiledRegexp

	return &svc, nil
}

type services []*Service
