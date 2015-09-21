package main

import (
	"encoding/json"
	"fmt"
	etcd "github.com/coreos/etcd/client"
	"golang.org/x/net/context"
	"time"
)

//go:generate ffjson $GOFILE

type Backend struct {
	Type string
}

const backendPathPattern = "%s/backends/%s/backend"
const serverTTL = 60 * time.Second

func (b *Backend) put(ctx context.Context, client etcd.KeysAPI, vulcanpath, servicename string) error {
	myContext, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	key := fmt.Sprintf(backendPathPattern, vulcanpath, servicename)
	be, err := json.Marshal(b)
	if err != nil {
		return err
	}

	_, err = client.Set(myContext, key, fmt.Sprintf("%s", be), &etcd.SetOptions{TTL: 0, PrevExist: etcd.PrevIgnore})
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

func (s *Server) put(ctx context.Context, client etcd.KeysAPI, vulcanpath, servicename, instancename string) error {
	myContext, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	key := fmt.Sprintf(serverPathPattern, vulcanpath, servicename, instancename)
	se, err := json.Marshal(s)
	if err != nil {
		return err
	}

	_, err = client.Set(myContext, key, fmt.Sprintf("%s", se), &etcd.SetOptions{TTL: serverTTL, PrevExist: etcd.PrevIgnore})
	return err
}
