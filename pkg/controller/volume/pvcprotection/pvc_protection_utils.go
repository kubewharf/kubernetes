package pvcprotection

import "sync"

type podLatestResourceVersion interface {
	Update(string) error
	Get() string
}

type PodLatestResourceVersion struct {
	ResourceVersion string

	mutex sync.Mutex
}

func NewPodLatestResourceVersion() podLatestResourceVersion {
	return &PodLatestResourceVersion{ResourceVersion: "0"}
}

func (p *PodLatestResourceVersion) Update(current string) error {
	p.mutex.Lock()
	p.ResourceVersion = current
	defer p.mutex.Unlock()
	return nil
}

func (p *PodLatestResourceVersion) Get() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.ResourceVersion
}
