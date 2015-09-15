package main // import "github.com/christian-blades-cb/desoto"

import (
	"errors"
	log "github.com/Sirupsen/logrus"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	"github.com/jessevdk/go-flags"
	"net/http"
	_ "net/http/pprof"
	"strings"
	"time"
)

var opts struct {
	Verbose func() `short:"v" description:"so many logs"`

	EtcdHosts             []string `short:"e" long:"etcd-hosts" description:"etcd host(s)" default:"http://localhost:4001"`
	VulcandEtcdBase       string   `long:"vulcand-basepath" description:"base path in etcd for vulcand entries" default:"/vulcand"`
	ServiceDefinitionBase string   `long:"servicedef-basepath" description:"base path in etcd for service definitions" default:"/publication"`

	DockerPath string `short:"d" long:"docker-path" description:"docker path" default:"unix:///var/run/docker.sock"`

	Host string `short:"h" long:"hostname" description:"external hostname, used for registering application to vulcand (in order to be useful, this hostname must be routable from vulcand)" default:"localhost"`
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

	go func() {
		log.Info(http.ListenAndServe("0.0.0.0:6060", nil))
	}()

	log.WithField("hosts", opts.EtcdHosts).Info("connecting to etcd")
	etcdClient := etcd.NewClient(opts.EtcdHosts)
	etcdClient.CreateDir(opts.ServiceDefinitionBase, 0)

	log.WithField("host", opts.DockerPath).Info("connecting to docker")
	dockerClient := mustGetDockerClient(opts.DockerPath)
	_ = dockerClient

	log.Info("setting up backends")
	svcs := mustGetServices(etcdClient, &opts.ServiceDefinitionBase)
	initializeVulcandBackends(etcdClient, opts.VulcandEtcdBase, svcs)
	log.Info("initial pass")
	updateVulcanDFromDocker(dockerClient, etcdClient, &opts.VulcandEtcdBase, svcs)

	ticker := time.NewTicker(30 * time.Second)

	defChange := make(chan bool)
	mustWatchServiceDefs(etcdClient, &opts.ServiceDefinitionBase, defChange)

	log.Info("beginning watch")
	// NOTE: never deletes backends, so orphans will need to be removed manually
	for {
		select {
		case <-defChange:
			log.Info("detected change to service definitions")
			svcs = mustGetServices(etcdClient, &opts.ServiceDefinitionBase)
			initializeVulcandBackends(etcdClient, opts.VulcandEtcdBase, svcs)
			updateVulcanDFromDocker(dockerClient, etcdClient, &opts.VulcandEtcdBase, svcs)
		case <-ticker.C:
			log.Debug("tick")
			updateVulcanDFromDocker(dockerClient, etcdClient, &opts.VulcandEtcdBase, svcs)
		}
	}
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

func updateVulcanDFromDocker(dclient *docker.Client, eclient *etcd.Client, vulcanPath *string, svcs services) {
	containers, err := dclient.ListContainers(docker.ListContainersOptions{})
	if err != nil {
		log.WithField("error", err).Fatal("unable to list running docker containers")
	}

	for _, c := range containers {
		for _, name := range c.Names {
			cleanName := strings.TrimLeft(name, "/")
			for _, s := range svcs {
				if s.re.MatchString(cleanName) {
					registerContainerWithVulcan(eclient, s, &c, vulcanPath, cleanName)
				}
			}
		}
	}

}

func registerContainerWithVulcan(client *etcd.Client, svc *Service, container *docker.APIContainers, vulcanPath *string, instanceName string) {
	port, err := findExternalPort(container, svc.serviceDef.ContainerPort)
	if err != nil {
		log.WithFields(log.Fields{
			"error":          err,
			"service":        svc.key,
			"container":      container.ID,
			"container_name": instanceName,
			"container_port": svc.serviceDef.ContainerPort,
		}).Warn("could not find exposed port")
		return
	}

	server := newServer(opts.Host, port)
	if err = server.put(client, *vulcanPath, svc.key, instanceName); err != nil {
		log.WithFields(log.Fields{
			"error":          err,
			"service":        svc.key,
			"container":      container.ID,
			"container_name": instanceName,
		}).Warn("could not add container to server registry")
	}
}

var PortNotExposedError = errors.New("container does not expose the specified port")

func findExternalPort(container *docker.APIContainers, containerPort int64) (int64, error) {
	for _, aPort := range container.Ports {
		if containerPort == aPort.PrivatePort {
			return aPort.PublicPort, nil
		}
	}
	return -1, PortNotExposedError
}

func initializeVulcandBackends(client *etcd.Client, basepath string, svcs services) {
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

// non-blocking
func mustWatchServiceDefs(client *etcd.Client, basepath *string, changed chan<- bool) {
	receiver := make(chan *etcd.Response)
	go func() {
		for {
			<-receiver
			changed <- true
		}
	}()

	go func() {
		_, err := client.Watch(*basepath, 0, true, receiver, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"error":       err,
				"servicepath": basepath,
			}).Fatal("could not start watch on service definitions")
		}
	}()
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
