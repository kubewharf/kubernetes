load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = [
        "doc.go",
        "err.go",
        "leader_ctrl.go",
        "mock_subconn.go",
        "picker.go",
        "roundrobin_balanced.go",
        "rw_separated.go",
        "singleflight.go",
    ],
    importmap = "k8s.io/kubernetes/vendor/go.etcd.io/etcd/clientv3/balancer/picker",
    importpath = "go.etcd.io/etcd/clientv3/balancer/picker",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/github.com/golang/mock/gomock:go_default_library",
        "//vendor/go.uber.org/zap:go_default_library",
        "//vendor/go.uber.org/zap/zapcore:go_default_library",
        "//vendor/google.golang.org/grpc/balancer:go_default_library",
        "//vendor/google.golang.org/grpc/connectivity:go_default_library",
        "//vendor/google.golang.org/grpc/resolver:go_default_library",
        "//vendor/google.golang.org/grpc/serviceconfig:go_default_library",
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
