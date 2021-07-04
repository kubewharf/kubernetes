package nadvisor

type NodeAdvisor struct {
	NumaTopology []Numa
}

type Numa struct {
	Id     int    `json:"numa_id"`
	Memory uint64 `json:"memory"`
	Cores  []Core `json:"cores"`
}

type Core struct {
	Id      int     `json:"core_id"`
	Threads []int   `json:"thread_ids"`
	Caches  []Cache `json:"caches"`
}

type Cache struct {
	// Size of memory cache in bytes.
	Size uint64 `json:"size"`
	// Type of memory cache: data, instruction, or unified.
	Type string `json:"type"`
	// Level (distance from cpus) in a multi-level cache hierarchy.
	Level int `json:"level"`
}

func (self *Numa) FindCore(id int) (bool, int) {
	for i, n := range self.Cores {
		if n.Id == id {
			return true, i
		}
	}
	return false, -1
}

func (self *Numa) AddThread(thread, core int) {
	var coreIdx int
	if core == -1 {
		// Assume one hyperthread per core when topology data is missing.
		core = thread
	}
	ok, coreIdx := self.FindCore(core)

	if !ok {
		// New core
		core := Core{Id: core}
		self.Cores = append(self.Cores, core)
		coreIdx = len(self.Cores) - 1
	}
	self.Cores[coreIdx].Threads = append(self.Cores[coreIdx].Threads, thread)
}

func (self *Numa) AddPerCoreCache(c Cache) {
	for idx := range self.Cores {
		self.Cores[idx].Caches = append(self.Cores[idx].Caches, c)
	}
}
