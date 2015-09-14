package main // import "github.com/christian-blades-cb/lewisandclark"

import (
	"errors"
	log "github.com/Sirupsen/logrus"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"github.com/jessevdk/go-flags"
	"strings"
	"time"
)

var opts struct {
	Verbose func() `short:"v" description:"so many logs"`

	EtcdHosts             []string `short:"e" long:"etcd-hosts" description:"etcd host(s)" default:"localhost:4001"`
	VulcandEtcdBase       string   `long:"vulcand-basepath" description:"base path in etcd for vulcand entries" default:"/vulcand"`
	ServiceDefinitionBase string   `long:"servicedef-basepath" description:"base path in etcd for service definitions" default:"/publication"`

	DockerPath string `short:"d" long:"docker-path" description:"docker path" default:"unix:///var/run/docker.sock"`

	Host string `short:"h" long:"hostname" description:"external hostname, used for registering application to vulcand" default:"localhost"`
}

func init() {
	opts.Verbose = func() {
		log.SetLevel(log.DebugLevel)
	}

}

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		log.Fatal("could not parse command line arguments")
	}

	etcdClient := etcd.NewClient(opts.EtcdHosts)
	etcdClient.CreateDir(opts.ServiceDefinitionBase, 0)

	dockerClient := mustGetDockerClient(opts.DockerPath)
	_ = dockerClient

	// start timer
	// start service definition watch
	// switch loop, update poll and update vulcand on either trigger
	ticker := time.NewTicker(30 * time.Second)
	for _ = range ticker.C {
		updateVulcanDFromDocker(dockerClient, etcdClient, &opts.VulcandEtcdBase, &opts.ServiceDefinitionBase)
	}

	// watch docker for events
	// load service definitions
	// match def to container
	// update specific backend in vulcand
}

func mustGetDockerClient(path string) *docker.Client {
	client, err := docker.NewClient(path)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  path,
			"error": err,
		}).Fatal("unable to connect to docker")
	}

	return client
}

// list running containers
// list service definitions
// match and map container name to service definition (deal with instance name)
// create/update vulcand backends
func updateVulcanDFromDocker(dclient *docker.Client, eclient *etcd.Client, vulcanPath, servicePath *string) {
	containers, err := dclient.ListContainers(docker.ListContainersOptions{})
	if err != nil {
		log.WithField("error", err).Fatal("unable to list running docker containers")
	}

	svcs := mustGetServices(eclient, servicePath)
	setupVulcandBackends(eclient, *vulcanPath, svcs)

	for _, c := range containers {
		for _, name := range c.Names {
			cleanName := strings.TrimLeft(name, "/")
			for _, s := range svcs {
				if s.re.MatchString(cleanName) {
					registerContainerWithVulcan(eclient, s, &c, cleanName)
				}
			}
		}
	}

}

func registerContainerWithVulcan(client *etcd.Client, svc *Service, container *docker.APIContainers, instanceName string) {
	port, err := findExternalPort(container, svc.serviceDef.ContainerPort)
	if err != nil {
		log.WithFields(log.Fields{
			"error":          err,
			"service":        svc.key,
			"container":      container.ID,
			"container_name": instanceName,
			"container_port": svc.serviceDef.ContainerPort,
		}).Warn("could not find exposed port")
	}

	server := newServer(opts.Host, port)
	if err = server.put(client, opts.VulcandEtcdBase, svc.key, instanceName); err != nil {
		log.WithFields(log.Fields{
			"error":          err,
			"service":        svc.key,
			"container":      container.ID,
			"container_name": instanceName,
		}).Warn("could not add container to server registry")
	}
}

func findExternalPort(container *docker.APIContainers, containerPort int64) (int64, error) {
	for _, aPort := range container.Ports {
		if containerPort == aPort.PrivatePort {
			return aPort.PublicPort, nil
		}
	}
	return -1, PortNotExposedError
}

var PortNotExposedError = errors.New("container does not expose the specified port")

func setupVulcandBackends(client *etcd.Client, basepath string, svcs services) {
	for _, s := range svcs {
		backend := Backend{Type: "http"}
		if err := backend.put(client, basepath, s.key); err != nil {
			log.WithFields(log.Fields{
				"error":   err,
				"service": s.key,
			}).Warn("could not register backend")
		}
	}
}

func watchServiceDefs(client *etcd.Client, basepath *string) {

}

func mustGetServices(client *etcd.Client, basepath *string) services {
	resp, err := client.Get(*basepath, false, true)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"basepath": basepath,
		}).Fatal("unable to get service definitions from etcd")
	}

	var svcs services
	for _, node := range resp.Node.Nodes {
		s, err := newService(node.Key, []byte(node.Value))
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err,
				"basepath": basepath,
				"key":      node.Key,
			}).Warn("invalid service definition. skipping.")
		} else {
			svcs = append(svcs, s)
		}
	}

	return svcs
}
