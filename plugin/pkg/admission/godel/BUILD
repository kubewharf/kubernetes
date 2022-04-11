load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["util.go"],
    importpath = "k8s.io/kubernetes/plugin/pkg/admission/godel",
    visibility = ["//visibility:public"],
    deps = ["//pkg/apis/core:go_default_library"],
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
        "//plugin/pkg/admission/godel/podannotations:all-srcs",
        "//plugin/pkg/admission/godel/podpriority:all-srcs",
    ],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
