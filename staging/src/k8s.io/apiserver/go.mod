// This is a generated file. Do not edit directly.

module k8s.io/apiserver

go 1.13

require (
	code.byted.org/tce/kubebrain-client v0.2.0
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/BurntSushi/toml v0.4.1 // indirect
	github.com/coreos/go-oidc v2.1.0+incompatible
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd v0.0.0-20190321100706-95778dfbb74e
	github.com/coreos/pkg v0.0.0-20180108230652-97fdf19511ea
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/docker v0.7.3-0.20190327010347-be7ac8be2ae0
	github.com/dustin/go-humanize v1.0.0 // indirect
	github.com/emicklei/go-restful v2.9.5+incompatible
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.3 // indirect
	github.com/go-openapi/spec v0.20.0
	github.com/go-openapi/swag v0.19.12 // indirect
	github.com/gogo/protobuf v1.3.1
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.1.0
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.1.0
	github.com/gorilla/websocket v1.4.0 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/golang-lru v0.5.1
	github.com/mailru/easyjson v0.7.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822
	github.com/pkg/errors v0.9.1
	github.com/pquerna/cachecontrol v0.1.0 // indirect
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/client_model v0.2.0
	github.com/sirupsen/logrus v1.6.0 // indirect
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.6.1
	go.etcd.io/etcd v0.5.0-alpha.5.0.20220511070715-6ab1e92a1634
	go.uber.org/zap v1.10.0
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/net v0.0.0-20210813160813-60bc85c4be6d
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/grpc v1.27.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	gopkg.in/square/go-jose.v2 v2.2.2
	gopkg.in/yaml.v2 v2.4.0
	gotest.tools v2.2.0+incompatible // indirect
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/client-go v0.0.0
	k8s.io/component-base v0.0.0
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6
	k8s.io/utils v0.0.0-20210616064148-7505c2c40546
	sigs.k8s.io/apiserver-network-proxy/konnectivity-client v0.0.7
	sigs.k8s.io/structured-merge-diff/v3 v3.0.0
	sigs.k8s.io/yaml v1.2.0
)

replace (
	cloud.google.com/go => cloud.google.com/go v0.38.0
	github.com/BurntSushi/toml => github.com/BurntSushi/toml v0.3.1
	github.com/go-openapi/jsonpointer => github.com/go-openapi/jsonpointer v0.19.3
	github.com/go-openapi/spec => github.com/go-openapi/spec v0.19.3
	github.com/go-openapi/swag => github.com/go-openapi/swag v0.19.5
	github.com/golang/protobuf => github.com/golang/protobuf v1.3.2
	github.com/google/go-cmp => github.com/google/go-cmp v0.3.0
	github.com/json-iterator/go => github.com/json-iterator/go v1.1.8
	github.com/kr/text => github.com/kr/text v0.1.0
	github.com/matttproud/golang_protobuf_extensions => github.com/matttproud/golang_protobuf_extensions v1.0.1
	github.com/pquerna/cachecontrol => github.com/pquerna/cachecontrol v0.0.0-20171018203845-0dec1b30a021
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.0.0
	github.com/prometheus/common => github.com/prometheus/common v0.4.1
	github.com/prometheus/procfs => github.com/prometheus/procfs v0.0.2
	github.com/sirupsen/logrus => github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify => github.com/stretchr/testify v1.4.0
	go.etcd.io/etcd => code.byted.org/tce/etcd v0.5.0-alpha.5.0.20220511070715-6ab1e92a1634 //  6ab1e92a1634 is the SHA of commit after git tag v3.4.3
	golang.org/x/sync => golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/text => golang.org/x/text v0.3.2
	golang.org/x/time => golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	google.golang.org/appengine => google.golang.org/appengine v1.5.0
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20190819201941-24fa4b261c55
	google.golang.org/grpc => google.golang.org/grpc v1.26.0
	gopkg.in/check.v1 => gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	gopkg.in/yaml.v2 => gopkg.in/yaml.v2 v2.2.8
	k8s.io/api => ../api
	k8s.io/apimachinery => ../apimachinery
	k8s.io/apiserver => ../apiserver
	k8s.io/client-go => ../client-go
	k8s.io/component-base => ../component-base
	k8s.io/utils => code.byted.org/tce/k8s-utils v0.0.0-20210616064148-7505c2c40546
)
