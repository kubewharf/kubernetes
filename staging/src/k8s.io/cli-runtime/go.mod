// This is a generated file. Do not edit directly.

module k8s.io/cli-runtime

go 1.13

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/emicklei/go-restful v2.9.5+incompatible // indirect
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-openapi/jsonreference v0.19.3 // indirect
	github.com/go-openapi/spec v0.19.3 // indirect
	github.com/google/btree v1.0.0 // indirect
	github.com/googleapis/gnostic v0.1.0
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de
	github.com/mailru/easyjson v0.7.0 // indirect
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	golang.org/x/text v0.3.2
	google.golang.org/appengine v1.5.0 // indirect
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/api v0.18.10
	k8s.io/apimachinery v0.18.10
	k8s.io/client-go v0.18.10
	k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6 // release-1.18
	sigs.k8s.io/kustomize v2.0.3+incompatible
	sigs.k8s.io/yaml v1.2.0
)

replace (
	github.com/google/go-cmp => github.com/google/go-cmp v0.3.0
	github.com/kr/text => github.com/kr/text v0.1.0
	github.com/pkg/errors => github.com/pkg/errors v0.8.1
	github.com/stretchr/testify => github.com/stretchr/testify v1.4.0
	golang.org/x/sys => golang.org/x/sys v0.0.0-20190813064441-fde4db37ae7a // pinned to release-branch.go1.13
	golang.org/x/tools => golang.org/x/tools v0.0.0-20190821162956-65e3620a7ae7 // pinned to release-branch.go1.13
	gopkg.in/check.v1 => gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127
	k8s.io/api => ../api
	k8s.io/apimachinery => ../apimachinery
	k8s.io/cli-runtime => ../cli-runtime
	k8s.io/client-go => ../client-go
	k8s.io/utils => code.byted.org/tce/k8s-utils v0.0.0-20201125131702-a289a73e4c95
)
