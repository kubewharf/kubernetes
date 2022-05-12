package factory

import (
	"sync"

	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/db"
	kubebrainClient "k8s.io/apiserver/pkg/storage/storagebackend/db/client/kubebrain"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/klog"
)

func newKubeBrainStorage(c storagebackend.Config) (storage.Interface, DestroyFunc, error) {
	client, err := kubebrainClient.NewBrainClient(c)
	if err != nil {
		klog.Errorf("new brain client failed %v", err)
		return nil, nil, err
	}

	var once sync.Once
	destroyFunc := func() {
		// we know that storage destroy funcs are called multiple times (due to reuse in subresources).
		// Hence, we only destroy once.
		// TODO: fix duplicated storage destroy calls higher level
		once.Do(func() {
			client.Close()
		})
	}
	transformer := c.Transformer
	if transformer == nil {
		transformer = value.IdentityTransformer
	}
	return db.New(client, c.Codec, c.Prefix, transformer, c.Paging, c.ETCDMaxLimit), destroyFunc, nil
}
