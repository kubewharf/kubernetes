load("@io_bazel_rules_go//go:def.bzl", "go_library", "go_test")

go_library(
    name = "go_default_library",
    srcs = [
        "logger.go",
        "store.go",
        "tracer.go",
    ],
    importpath = "k8s.io/kubernetes/third_party/kube-tracing",
    visibility = ["//visibility:public"],
    deps = [
        "//vendor/github.com/opentracing/opentracing-go:go_default_library",
        "//vendor/github.com/opentracing/opentracing-go/log:go_default_library",
        "//vendor/github.com/uber/jaeger-client-go:go_default_library",
        "//vendor/github.com/uber/jaeger-client-go/config:go_default_library",
    ],
)

go_test(
    name = "go_default_test",
    srcs = [
        "store_test.go",
        "tracer_test.go",
    ],
    embed = [":go_default_library"],
    deps = ["//vendor/github.com/opentracing/opentracing-go:go_default_library"],
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
        "//third_party/kube-tracing/dependencies/vendor/github.com/opentracing/opentracing-go:all-srcs",
        "//third_party/kube-tracing/dependencies/vendor/github.com/pkg/errors:all-srcs",
        "//third_party/kube-tracing/dependencies/vendor/github.com/uber/jaeger-client-go:all-srcs",
        "//third_party/kube-tracing/dependencies/vendor/github.com/uber/jaeger-lib/metrics:all-srcs",
    ],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
