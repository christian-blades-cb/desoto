package main

import (
	"encoding/json"
	"testing"
)

var keyA = "foobar"
var keyB = "/foobar/baz/leaf"

func BenchmarkNewService(b *testing.B) {
	var pattern string = `^etcd-app-\d+`
	var serviceDef = ServiceDefinition{
		Type:          "http",
		ContainerPort: 4001,
		NamePattern:   &pattern,
	}
	var serviceDefJson, _ = json.Marshal(serviceDef)

	for i := 0; i < b.N; i++ {
		if _, err := newService(keyB, serviceDefJson); err != nil {
			panic(err)
		}
	}
}
