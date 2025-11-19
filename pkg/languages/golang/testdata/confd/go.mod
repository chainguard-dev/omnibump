module github.com/iwilltry42/confd

go 1.20

require (
	bazil.org/fuse v0.0.0-20160811212531-371fbbdaa898 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.5.0 // indirect
	github.com/Nvveen/Gotty v0.0.0-20120604004816-cd527374f1e5 // indirect
	github.com/armon/consul-api v0.0.0-20180202201655-eb2c6b5be1b6 // indirect
	github.com/aws/aws-sdk-go v1.13.41
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/coreos/bbolt v1.3.2 // indirect
	github.com/coreos/etcd v3.3.10+incompatible
	github.com/coreos/go-etcd v2.0.0+incompatible // indirect
	github.com/coreos/go-semver v0.2.0 // indirect
	github.com/coreos/go-systemd v0.0.0-20190321100706-95778dfbb74e // indirect
	github.com/coreos/pkg v0.0.0-20180928190104-399ea9e2e55f // indirect
	github.com/cpuguy83/go-md2man v1.0.10 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.0 // indirect
	github.com/dgrijalva/jwt-go v3.2.0+incompatible // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/dustin/go-humanize v1.0.0 // indirect
	github.com/fsnotify/fsnotify v1.4.9
	github.com/garyburd/redigo v1.6.0
	github.com/go-ini/ini v1.36.0 // indirect
	github.com/gogo/protobuf v1.2.1 // indirect
	github.com/golang/groupcache v0.0.0-20190129154638-5b532d6fd5ef // indirect
	github.com/golang/protobuf v1.3.5 // indirect
	github.com/golang/snappy v0.0.0-20170215233205-553a64147049 // indirect
	github.com/google/btree v1.0.0 // indirect
	github.com/gorilla/websocket v1.4.0 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.0.0 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.9.0 // indirect
	github.com/hashicorp/consul v1.0.7
	github.com/hashicorp/errwrap v0.0.0-20141028054710-7554cd9344ce // indirect
	github.com/hashicorp/go-cleanhttp v0.0.0-20171218145408-d5fe4b57a186 // indirect
	github.com/hashicorp/go-multierror v0.0.0-20171204182908-b7773ae21874 // indirect
	github.com/hashicorp/go-rootcerts v0.0.0-20160503143440-6bb64b370b90 // indirect
	github.com/hashicorp/go-sockaddr v0.0.0-20180320115054-6d291a969b86 // indirect
	github.com/hashicorp/serf v0.8.1 // indirect
	github.com/hashicorp/vault v0.10.1
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/jmespath/go-jmespath v0.0.0-20160202185014-0b12d6b521d8 // indirect
	github.com/jonboulle/clockwork v0.1.0 // indirect
	github.com/kelseyhightower/memkv v0.1.1
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.1.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/opencontainers/runc v0.1.1 // indirect
	github.com/ory/dockertest v3.3.5+incompatible // indirect
	github.com/prometheus/client_golang v0.9.3 // indirect
	github.com/ryanuber/go-glob v0.0.0-20160226084822-572520ed46db // indirect
	github.com/samuel/go-zookeeper v0.0.0-20180130194729-c4fab1ac1bec
	github.com/sethgrid/pester v0.0.0-20180430140037-03e26c9abbbf // indirect
	github.com/sirupsen/logrus v1.7.0
	github.com/soheilhy/cmux v0.1.4 // indirect
	github.com/spf13/cast v1.3.0 // indirect
	github.com/spf13/cobra v0.0.3 // indirect
	github.com/spf13/pflag v1.0.3 // indirect
	github.com/spf13/viper v1.2.1 // indirect
	github.com/tmc/grpc-websocket-proxy v0.0.0-20190109142713-0ad062ec5ee5 // indirect
	github.com/ugorji/go v1.1.1 // indirect
	github.com/xiang90/probing v0.0.0-20190116061207-43a291ad63a2 // indirect
	github.com/xordataexchange/crypt v0.0.3-0.20170626215501-b2862e3d0a77
	go.etcd.io/bbolt v1.3.2 // indirect
	go.uber.org/atomic v1.4.0 // indirect
	go.uber.org/multierr v1.1.0 // indirect
	go.uber.org/zap v1.10.0 // indirect
	golang.org/x/net v0.0.0-20190522155817-f3200d17e092
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a // indirect
	golang.org/x/text v0.3.0 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	google.golang.org/grpc v1.21.0 // indirect
	gopkg.in/yaml.v2 v2.2.2
)

replace (
	github.com/coreos/bbolt => go.etcd.io/bbolt v1.3.6
	gopkg.in/ory-am/dockertest.v3 => github.com/ory/dockertest/v3 v3.7.0
)
