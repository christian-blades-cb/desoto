package main // import "github.com/christian-blades-cb/desoto"

import (
	"errors"
	log "github.com/Sirupsen/logrus"
	"github.com/cenkalti/backoff"
	etcd "github.com/coreos/etcd/client"
	"github.com/fsouza/go-dockerclient"
	"github.com/jessevdk/go-flags"
	"golang.org/x/net/context"
	"net/http"
	_ "net/http/pprof"
	"strings"
	"time"
)

var opts struct {
	Verbose func() `short:"v" long:"verbose" description:"so many logs"`

	EtcdHosts             []string `short:"e" long:"etcd-host" env:"ETCD_HOSTS" description:"etcd host(s)" default:"http://localhost:4001"`
	VulcandEtcdBase       string   `long:"vulcand-basepath" env:"VULCAND_PATH" description:"base path in etcd for vulcand entries" default:"/vulcand"`
	ServiceDefinitionBase string   `long:"servicedef-basepath" env:"SERVICEDEF_PATH" description:"base path in etcd for service definitions" default:"/publication"`

	DockerPath string `short:"d" long:"docker-host" env:"DOCKER_HOST" description:"docker path" default:"unix:///var/run/docker.sock"`

	Host string `short:"h" long:"hostname" env:"HOST" description:"external hostname, used for registering application to vulcand (in order to be useful, this hostname must be routable from vulcand)" default:"localhost"`
}

func init() {
	opts.Verbose = func() {
		log.SetLevel(log.DebugLevel)
	}
}

const DefaultTimeout = 5 * time.Second

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		log.Fatal("could not parse command line arguments")
	}

	go func() {
		log.Info(http.ListenAndServe("0.0.0.0:6060", nil))
	}()

	mainContext, mainCancel := context.WithCancel(context.Background())
	defer mainCancel()

	log.WithField("hosts", opts.EtcdHosts).Info("connecting to etcd")
	etcdCfg := etcd.Config{Endpoints: opts.EtcdHosts}

	eClient, err := etcd.New(etcdCfg)
	if err != nil {
		log.WithField("error", err).Fatal("unable to connect to etcd cluster")
	}
	kApi := etcd.NewKeysAPI(eClient)
	mustCreateServiceDirectory(mainContext, kApi, opts.ServiceDefinitionBase)

	log.WithField("host", opts.DockerPath).Info("connecting to docker")
	dockerClient := mustGetDockerClient(opts.DockerPath)
	_ = dockerClient

	log.Info("setting up backends")
	svcs := mustGetServices(mainContext, kApi, &opts.ServiceDefinitionBase)
	log.WithField("count", len(svcs)).Debug("found service definitions")
	initializeVulcandBackends(mainContext, kApi, opts.VulcandEtcdBase, svcs)
	log.Info("initial pass")
	updateVulcanDFromDocker(mainContext, dockerClient, kApi, &opts.VulcandEtcdBase, svcs)

	ticker := time.NewTicker(30 * time.Second)

	defChange := make(chan bool)
	mustWatchServiceDefs(mainContext, kApi, &opts.ServiceDefinitionBase, defChange)

	log.Info("beginning watch")
	// NOTE: never deletes backends, so orphans will need to be removed manually
	for {
		select {
		case <-defChange:
			log.Info("detected change to service definitions")
			svcs = mustGetServices(mainContext, kApi, &opts.ServiceDefinitionBase)
			log.WithField("count", len(svcs)).Debug("found service definitions")
			initializeVulcandBackends(mainContext, kApi, opts.VulcandEtcdBase, svcs)
			updateVulcanDFromDocker(mainContext, dockerClient, kApi, &opts.VulcandEtcdBase, svcs)
		case <-ticker.C:
			log.Debug("tick")
			updateVulcanDFromDocker(mainContext, dockerClient, kApi, &opts.VulcandEtcdBase, svcs)
		}
	}
}

func mustCreateServiceDirectory(ctx context.Context, kApi etcd.KeysAPI, basepath string) {
	myContext, myCancel := context.WithTimeout(ctx, DefaultTimeout)
	defer myCancel()

	_, err := kApi.Set(myContext, basepath, "", &etcd.SetOptions{Dir: true, PrevExist: etcd.PrevNoExist})
	log.WithField("error", err).Warn("error creating servicedef directory")
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

func updateVulcanDFromDocker(ctx context.Context, dclient *docker.Client, eclient etcd.KeysAPI, vulcanPath *string, svcs services) {
	containers, err := dclient.ListContainers(docker.ListContainersOptions{})
	if err != nil {
		log.WithField("error", err).Fatal("unable to list running docker containers")
	}

	for _, c := range containers {
		for _, name := range c.Names {
			cleanName := strings.TrimLeft(name, "/")
			for _, s := range svcs {
				if s.re.MatchString(cleanName) {
					log.WithField("container_name", cleanName).Debug("registering container as server")
					registerContainerWithVulcan(ctx, eclient, s, &c, vulcanPath, cleanName)
				}
			}
		}
	}

}

func registerContainerWithVulcan(ctx context.Context, client etcd.KeysAPI, svc *Service, container *docker.APIContainers, vulcanPath *string, instanceName string) {
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
	if err = server.put(ctx, client, *vulcanPath, svc.key, instanceName); err != nil {
		log.WithFields(log.Fields{
			"error":          err,
			"service":        svc.key,
			"container":      container.ID,
			"container_name": instanceName,
		}).Warn("could not add container to server registry")
	}
}

var PortNotExposedError = errors.New("container does not publicly expose the specified port")

func findExternalPort(container *docker.APIContainers, containerPort int64) (int64, error) {
	for _, aPort := range container.Ports {
		if containerPort == aPort.PrivatePort {
			// 0 is a "valid" port, but aPort.PublicPort == 0 if the port is not exposed on the host ¯\_(ツ)_/¯
			if aPort.PublicPort < 1 || aPort.PublicPort > 65535 {
				return -1, PortNotExposedError
			}

			return aPort.PublicPort, nil
		}
	}
	return -1, PortNotExposedError
}

func initializeVulcandBackends(ctx context.Context, client etcd.KeysAPI, basepath string, svcs services) {
	for _, s := range svcs {
		backend := Backend{Type: "http"}
		if err := backend.put(ctx, client, basepath, s.key); err != nil {
			log.WithFields(log.Fields{
				"error":   err,
				"service": s.key,
			}).Warn("could not register backend")
		}
	}
}

// non-blocking
func mustWatchServiceDefs(ctx context.Context, client etcd.KeysAPI, basepath *string, changed chan<- bool) {
	wOpts := &etcd.WatcherOptions{Recursive: true}
	watcher := client.Watcher(*basepath, wOpts)

	watchOperation := func() error {
		resp, err := watcher.Next(ctx)
		if err != nil {
			switch v := err.(type) {
			case etcd.Error:
				if v.Code == etcd.ErrorCodeEventIndexCleared {
					watcher = client.Watcher(*basepath, wOpts)

					log.WithFields(log.Fields{
						"basepath": *basepath,
						"code":     v.Code,
						"cause":    v.Cause,
						"index":    v.Index,
						"message":  v.Message,
					}).Warn("refreshed watcher")

					return nil
				}
			default:
				if err.Error() == "unexpected end of JSON input" {
					log.WithField("error", err).Warn("probably a connection timeout. are we in etcd 0.4.x?")
				} else {
					return err
				}
			}
		}

		if resp.Action != "get" {
			changed <- true
		}

		return nil
	}

	notify := func(err error, dur time.Duration) {
		log.WithFields(log.Fields{
			"dur":          dur,
			"error":        err,
			"service_path": *basepath,
		}).Error("service definition watch failed. backing off.")
	}

	go func() {
		for {
			err := backoff.RetryNotify(watchOperation, backoff.NewExponentialBackOff(), notify)
			if err != nil {
				log.WithFields(log.Fields{
					"error":        err,
					"service_path": *basepath,
				}).Fatal("unable to recover communication with etcd, watch abandoned")
			}
		}
	}()
}

func mustGetServices(ctx context.Context, client etcd.KeysAPI, basepath *string) services {
	resp, err := client.Get(ctx, *basepath, &etcd.GetOptions{Recursive: true})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"basepath": *basepath,
		}).Fatal("unable to get service definitions from etcd")
	}

	var svcs services
	for _, node := range resp.Node.Nodes {
		s, err := newService(node.Key, []byte(node.Value))
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err,
				"basepath": *basepath,
				"key":      node.Key,
			}).Warn("invalid service definition. skipping.")
		} else {
			svcs = append(svcs, s)
		}
	}

	return svcs
}
