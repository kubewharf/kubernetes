package kubewharf

import "github.com/onsi/ginkgo"

// KubewharfDescribe annotates the test with the Kubewharf label
func KubewharfDescribe(text string, body func()) bool {
	return ginkgo.Describe("[kubewharf] "+text, body)
}
