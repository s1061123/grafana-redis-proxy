# Grafana-redis-proxy (prototype)

# what is this?

This tool works as proxy between redis and grafana and enables grafana to visualize collectd's data, stored in redis with write_redis plugin. This tools supports REST API for grafana's [simple-json-datasource](https://github.com/bergquist/fake-simple-json-datasource).

![Grafana-redis-proxy sample usage](https://raw.githubusercontent.com/wiki/s1061123/grafana-redis-proxy/images/grafana-redis-proxy1.png)

# Build

`grafana-redis-proxy` is written in go, so following commands makes binary.

    go get github.com/s1061123/grafana-redis-proxy

# Options

`grafana-redis-proxy` takes two options to run: '-port' and '-redis'. '-redis' indicates the host running redis and '-port' indicates tcp port that `grafana-redis-proxy` listens to wait grafana simple json.

    # ./grafana-redis-proxy --help
    Usage of ./grafana-redis-proxy:
      -debug
            Print verbose message
      -port int
            Port for http server (default 8080)
      -redis string
            Host for redis server (default "localhost:6379")
