load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "client.go",
        "client_stream.go",
        "client_unary.go",
        "init.go",
        "interface.go",
        "leader.go",
        "options.go",
        "util.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/code.byted.org/tce/kubebrain-client/client",
    importpath = "code.byted.org/tce/kubebrain-client/client",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/code.byted.org/tce/kubebrain-client/api/v2rpc:go_default_library",
        "//vendor/code.byted.org/tce/kubebrain-client/client/balancer/picker:go_default_library",
        "//vendor/code.byted.org/tce/kubebrain-client/client/balancer/resolver:go_default_library",
        "//vendor/code.byted.org/tce/kubebrain-client/errors:go_default_library",
        "//vendor/github.com/google/uuid:go_default_library",
        "//vendor/github.com/pkg/errors:go_default_library",
        "//vendor/golang.org/x/sync/errgroup:go_default_library",
        "//vendor/google.golang.org/grpc:go_default_library",
        "//vendor/google.golang.org/grpc/balancer:go_default_library",
        "//vendor/google.golang.org/grpc/balancer/base:go_default_library",
        "//vendor/google.golang.org/grpc/codes:go_default_library",
        "//vendor/google.golang.org/grpc/health/grpc_health_v1:go_default_library",
        "//vendor/google.golang.org/grpc/resolver:go_default_library",
        "//vendor/google.golang.org/grpc/status:go_default_library",
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
    srcs = [
        ":package-srcs",
        "//vendor/code.byted.org/tce/kubebrain-client/client/balancer/picker:all-srcs",
        "//vendor/code.byted.org/tce/kubebrain-client/client/balancer/resolver:all-srcs",
    ],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
