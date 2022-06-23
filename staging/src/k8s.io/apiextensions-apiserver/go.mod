// This is a generated file. Do not edit directly.

module k8s.io/apiextensions-apiserver

go 1.13

require (
	github.com/emicklei/go-restful v2.9.5+incompatible
	github.com/go-openapi/errors v0.19.9
	github.com/go-openapi/spec v0.20.0
	github.com/go-openapi/strfmt v0.19.11
	github.com/go-openapi/validate v0.20.1
	github.com/gogo/protobuf v1.3.1
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.1.0
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.1.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.6.1
	go.etcd.io/etcd v0.5.0-alpha.5.0.20220511070715-6ab1e92a1634
	go.mongodb.org/mongo-driver v1.1.2 // indirect
	google.golang.org/grpc v1.27.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/apiserver v0.0.0
	k8s.io/client-go v0.0.0
	k8s.io/code-generator v0.0.0
	k8s.io/component-base v0.0.0
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6
	k8s.io/utils v0.0.0-20210616064148-7505c2c40546
	sigs.k8s.io/yaml v1.2.0
)

replace (
	cloud.google.com/go => cloud.google.com/go v0.38.0
	github.com/BurntSushi/toml => github.com/BurntSushi/toml v0.3.1
	github.com/go-openapi/analysis => github.com/go-openapi/analysis v0.19.5
	github.com/go-openapi/jsonpointer => github.com/go-openapi/jsonpointer v0.19.3
	github.com/go-openapi/loads => github.com/go-openapi/loads v0.19.4
	github.com/go-openapi/runtime => github.com/go-openapi/runtime v0.19.4
	github.com/go-openapi/spec => github.com/go-openapi/spec v0.19.3
	github.com/go-openapi/strfmt => github.com/go-openapi/strfmt v0.19.3
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
	golang.org/x/xerrors => golang.org/x/xerrors v0.0.0-20190717185122-a985d3407aa7
	google.golang.org/appengine => google.golang.org/appengine v1.5.0
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20190819201941-24fa4b261c55
	google.golang.org/grpc => google.golang.org/grpc v1.26.0
	gopkg.in/check.v1 => gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	gopkg.in/yaml.v2 => gopkg.in/yaml.v2 v2.2.8
	k8s.io/api => ../api
	k8s.io/apiextensions-apiserver => ../apiextensions-apiserver
	k8s.io/apimachinery => ../apimachinery
	k8s.io/apiserver => ../apiserver
	k8s.io/client-go => ../client-go
	k8s.io/code-generator => ../code-generator
	k8s.io/component-base => ../component-base
	k8s.io/utils => code.byted.org/tce/k8s-utils v0.0.0-20210616064148-7505c2c40546
)
