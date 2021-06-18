load("@io_bazel_rules_go//go:def.bzl", "go_library", "go_test")

go_library(
    name = "go_default_library",
    srcs = [
        "get_address_by_netlink.go",
        "pod_ip_annotations.go",
        "validate_tce_ip_envs.go",
    ],
    importpath = "k8s.io/kubernetes/pkg/kubelet/externals/hostdualstackip",
    visibility = ["//visibility:public"],
    deps = [
        "//pkg/kubelet/container:go_default_library",
        "//pkg/kubelet/lifecycle:go_default_library",
        "//staging/src/k8s.io/apimachinery/pkg/util/net:go_default_library",
        "//vendor/github.com/vishvananda/netlink:go_default_library",
        "//vendor/github.com/vishvananda/netlink/nl:go_default_library",
        "//vendor/k8s.io/klog:go_default_library",
    ],
)

go_test(
    name = "go_default_test",
    srcs = ["pod_ip_annotations_test.go"],
    embed = [":go_default_library"],
    deps = ["//pkg/kubelet/lifecycle:go_default_library"],
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
