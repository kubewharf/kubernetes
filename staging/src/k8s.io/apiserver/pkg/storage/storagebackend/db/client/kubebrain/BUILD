load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "client.go",
        "init.go",
        "watch.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/k8s.io/apiserver/pkg/storage/storagebackend/db/client/kubebrain",
    importpath = "k8s.io/apiserver/pkg/storage/storagebackend/db/client/kubebrain",
    visibility = ["//visibility:public"],
    deps = [
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/api:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/client:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/client/common:go_default_library",
        "//staging/src/k8s.io/apiserver/pkg/storage/storagebackend/db/common:go_default_library",
        "//vendor/code.byted.org/tce/kubebrain-client/api/v2rpc:go_default_library",
        "//vendor/code.byted.org/tce/kubebrain-client/client:go_default_library",
        "//vendor/google.golang.org/grpc/grpclog:go_default_library",
        "//vendor/k8s.io/klog:go_default_library",
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
    srcs = [":package-srcs"],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
