# DeSoto

Service discovery and publication for docker containers.

DeSoto is designed to solve the problem of publishing containerized services in a distributed cluster. An instance of DeSoto runs on each node in the cluster, watching for changes to service definitions and docker containers. It then updates the appropriate VulcanD backend, which handles routing incoming requests.

# Usage

## Service Definition

A service definition defines what type of vulcand backend (http, tcp, etc), what container port, and a container name pattern.

### Fields

* type - see [vulcand documentation](http://vulcanproxy.com/proxy.html#backends-and-servers)
* container port - which container port to publish*
* name_pattern - regex pattern for the container name (optional, defaults to /^\<key\>-app-\S+/)

\* container port MUST be mapped to a host port

### Example

```shell
$ docker ps
CONTAINER ID        IMAGE                      COMMAND                CREATED             STATUS              PORTS                                             NAMES
3e571a34a564        alpine:3.2                 "bundle exec rackup"   7 seconds ago       Up 2 seconds        0.0.0.0:25454->8080/tcp                           super-web-app-1

$ etcdctl set /publication/super-web '{ "type": "http", "container_port": 8080, "name_pattern": "^super-web-app-\\d+" }'
```

\* Note that regex escapes must be properly escaped for JSON
