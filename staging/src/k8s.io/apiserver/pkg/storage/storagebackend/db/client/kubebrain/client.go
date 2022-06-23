package kubebrain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"time"

	kubebrainClient "code.byted.org/tce/kubebrain-client/client"
	"github.com/pkg/errors"
	"k8s.io/klog"

	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/db/api"
	"k8s.io/apiserver/pkg/storage/storagebackend/db/client"
)

var _ client.Client = (*brainClient)(nil)

type brainClient struct {
	client kubebrainClient.Client
}

func NewBrainClient(config storagebackend.Config) (client.Client, error) {
	tlsConfig, err := getTLSConfig(config.Transport.CertFile, config.Transport.KeyFile, config.Transport.TrustedCAFile)
	if err != nil {
		return nil, err
	}
	client, err := kubebrainClient.NewClient(kubebrainClient.Config{
		Endpoints: config.Transport.ServerList,
		TLS:       tlsConfig,
		LogLevel:  8,
	})

	if err != nil {
		return nil, err
	}
	return &brainClient{client: client}, nil
}

func getTLSConfig(certFile, keyFile, ca string) (tlsConfig *tls.Config, err error) {
	if len(certFile) == 0 && len(keyFile) == 0 && len(ca) == 0 {
		klog.Info("conn to kubebrainv2 without tls config")
		return nil, nil
	}

	tlsConfig = &tls.Config{}

	if len(certFile) == 0 &&
		len(keyFile) == 0 {
		klog.Infof("conn to kubebrainv2 without client cert")
	} else {
		// load key pair
		// there will be an error if either keyFile or certFile is invalid (not given, not exist, empty, etc.)
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			klog.Error("can not load key pair cert=%s, key=%key, err=%s", certFile, keyFile, err.Error())
			return nil, errors.Wrapf(err, "can not load key pair")
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if len(ca) == 0 {
		klog.Infof("conn to kubebrainv2 with the host's root CA set")
	} else {
		// load ca file
		certPool := x509.NewCertPool()
		caFileBytes, err := ioutil.ReadFile(ca)
		if err != nil {
			klog.Error("can not load ca cert ca=%s, err=%s", ca, err.Error())
			return nil, errors.Wrapf(err, "can not load ca cert")
		}
		certPool.AppendCertsFromPEM(caFileBytes)
		tlsConfig.RootCAs = certPool
	}

	klog.Info("init tls config success")
	return tlsConfig, nil
}

func (b *brainClient) Close() error {
	return b.client.Close()
}

// Get implements client.Interface.Get.
func (b *brainClient) Get(ctx context.Context, key string) (int64, int64, []byte, error) {
	response, err := b.client.Get(ctx, key)
	if err != nil {
		return 0, 0, nil, err
	}
	// validate kv in response
	if response.Kv == nil {
		return int64(response.Header.Revision), 0, nil, nil
	}
	return int64(response.Header.Revision), int64(response.Kv.Revision), response.Kv.Value, nil
}

// Range implements client.Interface.Range.
func (b *brainClient) Range(ctx context.Context, key, end string, limit, revision int64) (int64, []*api.KeyValue, bool, int64, error) {
	var err error
	klog.Infof("[range stream] start range stream key=%s end=%s limit=%d revision=%d", key, end, limit, revision)
	start := time.Now()
	rev := int64(0)
	// todo to improve with grow slice
	var totalCount int64
	var more bool
	var kvs []*api.KeyValue
	if limit > 0 {
		resp, err := b.client.Range(ctx, key, end, kubebrainClient.WithRevision(uint64(revision)), kubebrainClient.WithLimit(limit))
		if err != nil {
			return 0, nil, false, 0, err
		}
		rev = int64(resp.Header.Revision)
		kvs = make([]*api.KeyValue, 0, len(resp.Kvs))
		for _, kv := range resp.Kvs {
			kvs = append(kvs, &api.KeyValue{
				Key:   string(kv.Key),
				Value: kv.Value,
				Rev:   int64(kv.Revision),
			})
		}
		more = resp.More
		totalCount = int64(len(kvs))
		if more {
			totalCount += 1
		}
	} else {
		kvs = make([]*api.KeyValue, 0)
		ch := b.client.RangeStream(ctx, key, end, kubebrainClient.WithLimit(limit), kubebrainClient.WithRevision(uint64(revision)))
		for resp := range ch {
			err = resp.Err()
			if err != nil {
				break
			}
			rev = int64(resp.Header.Revision)
			for _, kv := range resp.Kvs {
				kvs = append(kvs, &api.KeyValue{
					Key:   string(kv.Key),
					Value: kv.Value,
					Rev:   int64(kv.Revision),
				})
			}
		}
		klog.Infof("[range stream] summary range stream key %s end %s summary err %v, cost %f second", key, end, err, time.Now().Sub(start).Seconds())
		// range process need to be more precise
		totalCount = int64(len(kvs))
	}
	return rev, kvs, more, int64(totalCount), err
}

// Get implements client.Interface.Create.
func (b *brainClient) Create(ctx context.Context, key string, value []byte, ttl uint64) (bool, int64, error) {
	response, err := b.client.Create(ctx, key, string(value), kubebrainClient.WithTTL(int64(ttl)))
	if err != nil {
		klog.Errorf("brainClient Create method with key %s failed %v", key, err)
		return false, 0, err
	}
	if response.Header == nil {
		return false, 0, fmt.Errorf("empty response header in createResponse")
	}
	return response.Succeeded, int64(response.Header.Revision), nil
}

// Delete implements client.Interface.Delete.
func (b *brainClient) Delete(ctx context.Context, key string, revision int64) (bool, int64, []byte, error) {
	response, err := b.client.Delete(ctx, key, kubebrainClient.WithRevision(uint64(revision)))
	if err != nil {
		return false, 0, nil, err
	}
	// cas failed
	if !response.Succeeded {
		// key not found
		if response.Kv == nil {
			return false, 0, nil, err
		}
		return false, int64(response.Kv.Revision), response.Kv.Value, nil
	}
	return response.Succeeded, revision, nil, nil
}

// Update implements client.Interface.Update.
func (b *brainClient) Update(ctx context.Context, key string, revision int64, value []byte, ttl uint64) (bool, int64, []byte, error) {
	response, err := b.client.Update(ctx, key, string(value), uint64(revision), kubebrainClient.WithTTL(int64(ttl)))
	if err != nil {
		return false, 0, nil, err
	}
	// cas failed
	if !response.Succeeded {
		// key not exists
		if response.Kv == nil {
			return false, 0, nil, nil
		}
		return false, int64(response.Kv.Revision), response.Kv.Value, nil
	}
	// cas success
	return true, int64(response.Header.Revision), value, nil
}

// Count implements client.Interface.Count.
func (b *brainClient) Count(ctx context.Context, start, end string) (int64, error) {
	countResponse, err := b.client.Count(ctx, start, end)
	if err != nil {
		return 0, err
	}
	return int64(countResponse.Count), nil
}

func (b *brainClient) Name() string {
	return "kube-brain"
}

func (b *brainClient) Compact(ctx context.Context, revision int64) error {
	// kube-brain will invoke compact internal, no need to trigger compaction
	_, err := b.client.Compact(ctx, uint64(revision))
	return err
}

func (b *brainClient) DisableLimit() bool {
	return true
}
