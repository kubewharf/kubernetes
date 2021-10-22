// +build cgo linux darwin

/*
Copyright 2015 The Kubernetes Authors.

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

package cadvisor

import (
	"encoding/json"
	"flag"
	"net/http"
	"sync"
	"time"

	"k8s.io/klog"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
)

const (
	// maxHistory is max length of metrics keeped in the ring buffer.
	maxHistory = 10
	// thresholdMetPercentage is a percentage, beyond which a threshold considered to be exceeded.
	thresholdMetPercentage = 0.8
)

var (
	collectkeepingInterval = flag.Duration("collectkeeping_interval", 30*time.Second, "Interval between collect keepings")
	tceMetricsUrl          = flag.String("tceMetrics_url", "http://127.0.0.1:8087/load", "tceMetricsUrl")
	httpClient             = &http.Client{
		Timeout: 3 * time.Second,
	}
)

type tceMetricSnapShot struct {
	Info MetricsInfo
	Time int64
}

type tceMetricRing struct {
	mutex        sync.Mutex
	MaxLen       int
	Queue        []*tceMetricSnapShot
	CurrentIndex int
}

func createTceMetricRing(size int) *tceMetricRing {
	return &tceMetricRing{
		MaxLen:       size,
		Queue:        make([]*tceMetricSnapShot, size),
		CurrentIndex: -1,
		mutex:        sync.Mutex{},
	}
}

func (ring *tceMetricRing) Count(softLimit, hardLimit int64) (softOverCount, hardOverCount float64) {
	softOverCount = 0.0
	hardOverCount = 0.0
	ring.mutex.Lock()
	defer ring.mutex.Unlock()

	for _, snapshot := range ring.Queue {
		if snapshot == nil {
			continue
		}
		if utilfeature.DefaultFeatureGate.Enabled(features.DynamicLoadEvictionThreshold) && snapshot.Info.LowerBound > 0 {
			if snapshot.Info.Value > snapshot.Info.LowerBound {
				softOverCount++
			}
			if snapshot.Info.Value > snapshot.Info.UpperBound {
				hardOverCount++
			}
		} else {
			bound := snapshot.Info.LowerBound
			if bound == 0 {
				bound = snapshot.Info.UpperBound
			}
			if snapshot.Info.Value > bound*float64(softLimit)/100 {
				softOverCount++
			}
			if snapshot.Info.Value > bound*float64(hardLimit)/100 {
				hardOverCount++
			}
		}
	}
	return
}

func (ring *tceMetricRing) Sum() float64 {
	ring.mutex.Lock()
	defer ring.mutex.Unlock()
	sum := 0.0
	for _, snapshot := range ring.Queue {
		if snapshot != nil {
			sum += snapshot.Info.Value
		}
	}
	return sum
}

func (ring *tceMetricRing) Push(snapShot *tceMetricSnapShot) {
	ring.mutex.Lock()
	defer ring.mutex.Unlock()
	if ring.CurrentIndex != -1 && snapShot != nil {
		latestSnapShot := ring.Queue[ring.CurrentIndex]
		if latestSnapShot != nil && latestSnapShot.Time == snapShot.Time {
			return
		}
	}

	ring.CurrentIndex = (ring.CurrentIndex + 1) % ring.MaxLen
	ring.Queue[ring.CurrentIndex] = snapShot
}

type tceMetricsClient struct {
	mutex         sync.RWMutex
	MetricHistory map[string]*tceMetricRing
	quitChannels  []chan error
}

func NewTceMetricsClient() TCEInterface {
	//metricHistory := make([]MetricsInfo, maxHistory)
	return &tceMetricsClient{
		mutex:         sync.RWMutex{},
		MetricHistory: make(map[string]*tceMetricRing),
		quitChannels:  make([]chan error, 0, 1),
	}
}

func (tmc *tceMetricsClient) Start() error {
	quitGlobalHousekeeping := make(chan error)
	tmc.quitChannels = append(tmc.quitChannels, quitGlobalHousekeeping)
	go tmc.collectKeeping(quitGlobalHousekeeping)
	return nil
}

func (tmc *tceMetricsClient) Stop() error {
	for i, c := range tmc.quitChannels {
		// Send the exit signal and wait on the thread to exit (by closing the channel).
		c <- nil
		err := <-c
		if err != nil {
			// Remove the channels that quit successfully.
			tmc.quitChannels = tmc.quitChannels[i:]
			return err
		}
	}
	tmc.quitChannels = make([]chan error, 0, 1)
	return nil
}

func (tmc *tceMetricsClient) ThresholdsMet(softLimit int64, hardLimit int64) (bool, bool) {
	metricRing, ok := tmc.getMetricHistory("system")
	if !ok {
		return false, false
	}
	isSoftOver, isHardOver := false, false
	softOverCount, hardOverCount := metricRing.Count(softLimit, hardLimit)
	if softOverCount/float64(metricRing.MaxLen) >= thresholdMetPercentage {
		isSoftOver = true
	}
	if hardOverCount/float64(metricRing.MaxLen) >= thresholdMetPercentage {
		isHardOver = true
	}
	return isSoftOver, isHardOver
}

func (tmc *tceMetricsClient) GetLoad(podname string) float64 {
	metricRing, ok := tmc.getMetricHistory(podname)
	if !ok {
		return 0.0
	}
	return metricRing.Sum()
}

func (tmc *tceMetricsClient) getMetricHistory(name string) (*tceMetricRing, bool) {
	tmc.mutex.RLock()
	defer tmc.mutex.RUnlock()
	metricRing, ok := tmc.MetricHistory[name]
	return metricRing, ok
}

func (tmc *tceMetricsClient) emptyPush() {
	tmc.mutex.Lock()
	defer tmc.mutex.Unlock()
	for _, metricRing := range tmc.MetricHistory {
		metricRing.Push(nil)
	}
}

func (tmc *tceMetricsClient) collectMetric() error {
	var rawMetrics TCEMetrics
	resp, err := httpClient.Get(*tceMetricsUrl)
	if err != nil {
		tmc.emptyPush()
		return err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&rawMetrics)
	if err != nil {
		tmc.emptyPush()
		return err
	}
	for _, item := range rawMetrics.Items {
		if item.Type == "system" || item.Type == "pod" {
			name := item.Name
			if item.Type == "system" {
				name = "system"
			}
			metricRing, ok := tmc.getMetricHistory(name)
			if !ok {
				metricRing = createTceMetricRing(maxHistory)
				tmc.mutex.Lock()
				tmc.MetricHistory[name] = metricRing
				tmc.mutex.Unlock()
			}
			for _, info := range item.Infos {
				if info.Name == "load.1min.system" || info.Name == "load.1min.entity" {
					snapShot := &tceMetricSnapShot{
						Info: info,
						Time: rawMetrics.Time,
					}
					metricRing.Push(snapShot)
					break
				}
			}
		}
	}
	return nil
}

func (tmc *tceMetricsClient) collectKeeping(quit chan error) {
	// Long housekeeping is either 100ms or half of the housekeeping interval.
	longCollectkeeping := 100 * time.Millisecond

	ticker := time.Tick(*collectkeepingInterval)
	for {
		select {
		case t := <-ticker:
			start := time.Now()
			err := tmc.collectMetric()
			if err != nil {
				klog.Warningf("Failed to collect tce metrics, err %v", err)
			}
			// Log if housekeeping took too long.
			duration := time.Since(start)
			if duration >= longCollectkeeping {
				klog.V(3).Infof("TCE Collectkeeping(%d) took %s", t.Unix(), duration)
			}
		case <-quit:
			// Quit if asked to do so.
			quit <- nil
			klog.Infof("Exiting TCE collectKeeping thread")
			return
		}
	}
}
