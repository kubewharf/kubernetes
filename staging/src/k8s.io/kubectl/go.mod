// This is a generated file. Do not edit directly.

module k8s.io/kubectl

go 1.13

require (
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/MakeNowJust/heredoc v0.0.0-20170808103936-bb23615498cd
	github.com/chai2010/gettext-go v0.0.0-20160711120539-c6fed771bfd5
	github.com/davecgh/go-spew v1.1.1
	github.com/daviddengcn/go-colortext v0.0.0-20160507010035-511bcaf42ccd
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/docker v0.7.3-0.20190327010347-be7ac8be2ae0
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/exponent-io/jsonpath v0.0.0-20151013193312-d6023ce2651d
	github.com/fatih/camelcase v1.0.0
	github.com/go-openapi/spec v0.20.0
	github.com/golangplus/bytes v0.0.0-20160111154220-45c989fe5450 // indirect
	github.com/golangplus/fmt v0.0.0-20150411045040-2a5d6d7d2995 // indirect
	github.com/golangplus/testing v0.0.0-20180327235837-af21d9c3145e // indirect
	github.com/google/go-cmp v0.5.2
	github.com/googleapis/gnostic v0.1.0
	github.com/jonboulle/clockwork v0.1.0
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de
	github.com/lithammer/dedent v1.1.0
	github.com/mitchellh/go-wordwrap v1.0.0
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.7.0
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/russross/blackfriday v1.5.2
	github.com/sirupsen/logrus v1.6.0 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.6.1
	github.com/xlab/handysort v0.0.0-20150421192137-fb3537ed64a1 // indirect
	golang.org/x/sys v0.0.0-20210820121016-41cdb8703e55
	gopkg.in/yaml.v2 v2.4.0
	gotest.tools v2.2.0+incompatible // indirect
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/cli-runtime v0.0.0
	k8s.io/client-go v0.0.0
	k8s.io/component-base v0.0.0
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6
	k8s.io/metrics v0.0.0
	k8s.io/utils v0.0.0-20210616064148-7505c2c40546
	sigs.k8s.io/kustomize v2.0.3+incompatible
	sigs.k8s.io/yaml v1.2.0
	vbom.ml/util v0.0.0-20160121211510-db5cfe13f5cc
)

replace (
	cloud.google.com/go => cloud.google.com/go v0.38.0
	github.com/go-openapi/jsonpointer => github.com/go-openapi/jsonpointer v0.19.3
	github.com/go-openapi/spec => github.com/go-openapi/spec v0.19.3
	github.com/go-openapi/swag => github.com/go-openapi/swag v0.19.5
	github.com/golang/protobuf => github.com/golang/protobuf v1.3.2
	github.com/google/go-cmp => github.com/google/go-cmp v0.3.0
	github.com/json-iterator/go => github.com/json-iterator/go v1.1.8
	github.com/kr/text => github.com/kr/text v0.1.0
	github.com/matttproud/golang_protobuf_extensions => github.com/matttproud/golang_protobuf_extensions v1.0.1
	github.com/opencontainers/go-digest => github.com/opencontainers/go-digest v1.0.0-rc1
	github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.0.0
	github.com/prometheus/common => github.com/prometheus/common v0.4.1
	github.com/prometheus/procfs => github.com/prometheus/procfs v0.0.2
	github.com/sirupsen/logrus => github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify => github.com/stretchr/testify v1.4.0
	golang.org/x/sync => golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/text => golang.org/x/text v0.3.2
	golang.org/x/time => golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	golang.org/x/xerrors => golang.org/x/xerrors v0.0.0-20190717185122-a985d3407aa7
	google.golang.org/appengine => google.golang.org/appengine v1.5.0
	google.golang.org/grpc => google.golang.org/grpc v1.26.0
	gopkg.in/check.v1 => gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	gopkg.in/yaml.v2 => gopkg.in/yaml.v2 v2.2.8
	k8s.io/api => ../api
	k8s.io/apimachinery => ../apimachinery
	k8s.io/cli-runtime => ../cli-runtime
	k8s.io/client-go => ../client-go
	k8s.io/code-generator => ../code-generator
	k8s.io/component-base => ../component-base
	k8s.io/kubectl => ../kubectl
	k8s.io/metrics => ../metrics
	k8s.io/utils => code.byted.org/tce/k8s-utils v0.0.0-20210616064148-7505c2c40546
)
