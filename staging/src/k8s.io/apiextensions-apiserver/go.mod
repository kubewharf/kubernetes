// This is a generated file. Do not edit directly.

module k8s.io/apiextensions-apiserver

go 1.13

require (
	github.com/emicklei/go-restful v2.9.5+incompatible
	github.com/go-openapi/errors v0.19.2
	github.com/go-openapi/spec v0.19.3
	github.com/go-openapi/strfmt v0.19.3
	github.com/go-openapi/validate v0.19.5
	github.com/gogo/protobuf v1.3.1
	github.com/google/go-cmp v0.5.2
	github.com/google/gofuzz v1.1.0
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.1.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	go.etcd.io/etcd v0.0.0-20191023171146-3cf2f69b5738
	google.golang.org/grpc v1.26.0
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/api v0.18.10
	k8s.io/apimachinery v0.18.10
	k8s.io/apiserver v0.0.0
	k8s.io/client-go v0.18.10
	k8s.io/code-generator v0.18.16-rc.0
	k8s.io/component-base v0.0.0
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6 // release-1.18
	k8s.io/utils v0.0.0-20201125131702-a289a73e4c95
	sigs.k8s.io/yaml v1.2.0
)

replace (
	github.com/google/go-cmp => github.com/google/go-cmp v0.3.0
	github.com/kr/text => github.com/kr/text v0.1.0
	github.com/pkg/errors => github.com/pkg/errors v0.8.1
	github.com/stretchr/testify => github.com/stretchr/testify v1.4.0
	go.uber.org/atomic => go.uber.org/atomic v1.3.2
	golang.org/x/sys => golang.org/x/sys v0.0.0-20190813064441-fde4db37ae7a // pinned to release-branch.go1.13
	golang.org/x/tools => golang.org/x/tools v0.0.0-20190821162956-65e3620a7ae7 // pinned to release-branch.go1.13
	gopkg.in/check.v1 => gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	k8s.io/api => ../api
	k8s.io/apiextensions-apiserver => ../apiextensions-apiserver
	k8s.io/apimachinery => ../apimachinery
	k8s.io/apiserver => ../apiserver
	k8s.io/client-go => ../client-go
	k8s.io/code-generator => ../code-generator
	k8s.io/component-base => ../component-base
	k8s.io/utils => code.byted.org/tce/k8s-utils v0.0.0-20201125131702-a289a73e4c95
)
