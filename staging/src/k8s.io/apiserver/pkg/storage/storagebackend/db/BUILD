package(default_visibility = ["//visibility:public"])

load(
    "@io_bazel_rules_go//go:def.bzl",
    "go_library",
)

go_library(
    name = "go_default_library",
    srcs = [
        "errors.go",
        "store.go",
        "watcher.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/k8s.io/apiserver/pkg/storage/storagebackend/db",
    importpath = "k8s.io/apiserver/pkg/storage/storagebackend/db",
    deps = [
        "//staging/src/k8s.io/apimachinery/pkg/api/errors:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/api/meta:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/apis/meta/v1/unstructured:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/conversion:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/runtime:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/util/runtime:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/watch:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/features:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/etcd3/metrics:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/api:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/client:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/common:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/value:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/util/feature:go_default_library",
        "//vendor/go.etcd.io/etcd/etcdserver/api/v3rpc/rpctypes:go_default_library",
        "//vendor/k8s.io/klog:go_default_library",
        "//vendor/k8s.io/utils/trace:go_default_library",
    ],
)

filegroup(
    name = "package-srcs",
    srcs = glob(["**"]),
    tags = ["automanaged"],
    visibility = ["//visibility:private"],
)

filegroup(
    name = "all-srcs",
    srcs = [
        ":package-srcs",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/api:all-srcs",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/client:all-srcs",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/common:all-srcs",
    ],
    tags = ["automanaged"],
)
