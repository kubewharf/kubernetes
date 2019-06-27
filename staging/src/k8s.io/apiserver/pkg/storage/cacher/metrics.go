/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cacher

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	subsystem = "apiserver_storage"
)

var (
	cacheStatus = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "cache_ready",
			Help:      "Gauge of apiserver storage cache is ready.",
		})
	initSlowProcessingDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "apiserver_init_events_slowly_processing",
			Help:    "Duration of init events processed slowly in watchcache",
			Buckets: prometheus.ExponentialBuckets(100, 2, 12),
		},
	)
)

func init() {
	prometheus.MustRegister(cacheStatus)
	prometheus.MustRegister(initSlowProcessingDuration)
}

// ObserveCacheStatus updates the relevant prometheus metrics for the cache status.
func ObserveCacheStatus(status bool) {
	if status {
		cacheStatus.Set(1)
	} else {
		cacheStatus.Set(0)
	}
}
