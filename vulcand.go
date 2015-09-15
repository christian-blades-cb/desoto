package main

import (
	"encoding/json"
	"fmt"
	"github.com/coreos/go-etcd/etcd"
)

//go:generate ffjson $GOFILE

type Backend struct {
	Type string
}

const backendPathPattern = "%s/backends/%s/backend"

func (b *Backend) put(client *etcd.Client, vulcanpath, servicename string) error {
	key := fmt.Sprintf(backendPathPattern, vulcanpath, servicename)
	be, err := json.Marshal(b)
	if err != nil {
		return err
	}

	_, err = client.Set(key, fmt.Sprintf("%s", be), 0)
	return err
}

type Server struct {
	URL string
}

const serverPathPattern = "%s/backends/%s/servers/%s"

func newServer(host string, port int64) Server {
	return Server{
		URL: fmt.Sprintf("http://%s:%d", host, port),
	}
}

func (s *Server) put(client *etcd.Client, vulcanpath, servicename, instancename string) error {
	key := fmt.Sprintf(serverPathPattern, vulcanpath, servicename, instancename)
	se, err := json.Marshal(s)
	if err != nil {
		return err
	}

	_, err = client.Set(key, fmt.Sprintf("%s", se), 60)
	return err
}
