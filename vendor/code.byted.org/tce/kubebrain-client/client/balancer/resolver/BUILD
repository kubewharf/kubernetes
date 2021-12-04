load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["resolver.go"],
    importmap = "k8s.io/kubernetes/vendor/code.byted.org/tce/kubebrain-client/client/balancer/resolver",
    importpath = "code.byted.org/tce/kubebrain-client/client/balancer/resolver",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/google.golang.org/grpc/resolver:go_default_library",
        "//vendor/google.golang.org/grpc/resolver/dns:go_default_library",
        "//vendor/google.golang.org/grpc/resolver/passthrough:go_default_library",
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
