load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["logger.go"],
    importmap = "k8s.io/kubernetes/vendor/k8s.io/apiserver/pkg/storage/storagebackend/db/client/common",
    importpath = "k8s.io/apiserver/pkg/storage/storagebackend/db/client/common",
    visibility = ["//visibility:public"],
    deps = [
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
