load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "builder.go",
        "leader_ctrl.go",
        "rw_separated.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/code.byted.org/tce/kubebrain-client/client/balancer/picker",
    importpath = "code.byted.org/tce/kubebrain-client/client/balancer/picker",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/golang.org/x/sync/singleflight:go_default_library",
        "//vendor/google.golang.org/grpc/balancer:go_default_library",
        "//vendor/google.golang.org/grpc/balancer/base:go_default_library",
        "//vendor/google.golang.org/grpc/resolver:go_default_library",
        "//vendor/k8s.io/klog/v2:go_default_library",
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
