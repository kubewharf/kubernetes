load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["event.go"],
    importmap = "k8s.io/kubernetes/vendor/k8s.io/apiserver/pkg/storage/storagebackend/db/api",
    importpath = "k8s.io/apiserver/pkg/storage/storagebackend/db/api",
    visibility = ["//visibility:public"],
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
