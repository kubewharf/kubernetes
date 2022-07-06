package nadvisor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	"github.com/google/cadvisor/utils/sysfs"
	"github.com/google/cadvisor/utils/sysinfo"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

var (
	numaPath             = "/sys/devices/system/node/"
	numaRegExp           = regexp.MustCompile(`^node([0-9]+)$`)
	numaCpuRegExp        = regexp.MustCompile(`^cpu([0-9]+)$`)
	numaCoreRegExp       = regexp.MustCompile(`^([0-9]+)\n`)
	memoryCapacityRegexp = regexp.MustCompile(`MemTotal:\s*([0-9]+) kB`)
	sysVendorFile        = "/sys/devices/virtual/dmi/id/sys_vendor"
	numaTopologyFile     = "/opt/tiger/tce/topology.json"
)

func New() NodeAdvisor {
	sysFs := sysfs.NewRealSysFs()
	numaTopology, err := GetNumaTopology(sysFs, numaPath)
	if err != nil {
		klog.Errorf("Get numa topology failed: %v", err)
	}
	nodeAdvisor := NodeAdvisor{
		NumaTopology: numaTopology,
	}
	return nodeAdvisor
}

func GetNumaTopology(sysFs sysfs.SysFs, numaPath string) ([]Numa, error) {
	numas := []Numa{}

	lastThread := -1
	lastCore := -1
	lastNuma := -1

	numaDirs, err := ioutil.ReadDir(numaPath)
	if err != nil {
		return nil, err
	}
	for _, numaDir := range numaDirs {
		numaDirName := numaDir.Name()
		ok, val, err := extractValue(numaDirName, numaRegExp)
		if err != nil {
			return nil, fmt.Errorf("could not parse numa info from %s: %v", numaDirName, err)
		}
		if !ok {
			continue
		}
		lastNuma = val
		nodeIdx, err := addNuma(&numas, lastNuma)
		if err != nil {
			return nil, fmt.Errorf("failed to add numa %d: %v", lastNuma, err)
		}
		numaDirPath := path.Join(numaPath, numaDirName)
		cpuDirs, err := ioutil.ReadDir(numaDirPath)
		for _, cpuDir := range cpuDirs {
			cpuDirName := cpuDir.Name()
			ok, val, err := extractValue(cpuDirName, numaCpuRegExp)
			if err != nil {
				return nil, fmt.Errorf("could not parse cpu info from %s: %v", cpuDirName, err)
			}
			if !ok {
				continue
			}
			lastThread = val
			coreDirPath := path.Join(numaDirPath, cpuDirName, "/topology/core_id")
			out, err := ioutil.ReadFile(coreDirPath)
			if err != nil {
				return nil, fmt.Errorf("could not get core info from %s: %v", coreDirPath, err)
			}
			ok, val, err = extractValue(string(out), numaCoreRegExp)
			if err != nil {
				return nil, fmt.Errorf("could not parse core info from %s: %v", string(out), err)
			}
			if !ok {
				continue
			}
			lastCore = val
			numas[nodeIdx].AddThread(lastThread, lastCore)
		}
	}
	for idx, numa := range numas {
		caches, err := sysinfo.GetCacheInfo(sysFs, numa.Cores[0].Threads[0])
		if err != nil {
			klog.Errorf("failed to get cache information for numa %d: %v", numa.Id, err)
			continue
		}
		numThreadsPerCore := len(numa.Cores[0].Threads)
		for _, cache := range caches {
			c := Cache{
				Size:  cache.Size,
				Level: cache.Level,
				Type:  cache.Type,
			}
			if cache.Cpus == numThreadsPerCore {
				// Add to each core.
				numas[idx].AddPerCoreCache(c)
			}
			// Ignore unknown caches.
		}
	}
	return numas, nil
}

func extractValue(s string, r *regexp.Regexp) (bool, int, error) {
	matches := r.FindSubmatch([]byte(s))
	if len(matches) == 2 {
		val, err := strconv.ParseInt(string(matches[1]), 10, 32)
		if err != nil {
			return false, -1, err
		}
		return true, int(val), nil
	}
	return false, -1, nil
}

func addNuma(numas *[]Numa, id int) (int, error) {
	var idx int
	if id == -1 {
		// Some VMs don't fill topology data. Export single package.
		id = 0
	}

	ok, idx := findNuma(*numas, id)
	if !ok {
		// New node
		numa := Numa{Id: id}
		// Add per-node memory information.
		meminfo := fmt.Sprintf("/sys/devices/system/node/node%d/meminfo", id)
		out, err := ioutil.ReadFile(meminfo)
		// Ignore if per-node info is not available.
		if err == nil {
			m, err := parseCapacity(out, memoryCapacityRegexp)
			if err != nil {
				return -1, err
			}
			numa.Memory = uint64(m)
		}
		*numas = append(*numas, numa)
		idx = len(*numas) - 1
	}
	return idx, nil
}

func findNuma(numas []Numa, id int) (bool, int) {
	for i, n := range numas {
		if n.Id == id {
			return true, i
		}
	}
	return false, -1
}

func parseCapacity(b []byte, r *regexp.Regexp) (uint64, error) {
	matches := r.FindSubmatch(b)
	if len(matches) != 2 {
		return 0, fmt.Errorf("failed to match regexp in output: %q", string(b))
	}
	m, err := strconv.ParseUint(string(matches[1]), 10, 64)
	if err != nil {
		return 0, err
	}

	// Convert to bytes.
	return m * 1024, err
}

type PseudoNUMAData struct {
	Vendor   string     `json:"vendor"`
	Version  string     `json:"version"`
	Socket   int        `json:"socket"`
	NUMA     int        `json:"numa"`
	Topology CPUDetails `json:"topology,omitempty"`
}

// CPUInfo contains the socket and core IDs associated with a CPU.
type CPUInfo struct {
	SocketID int
	NumaID   int
	CoreID   int
}

// CPUDetails is a map from CPU ID to Core ID and Socket ID.
type CPUDetails map[int]CPUInfo

func GetRefinedTopology() (bool, []cadvisorapi.Node, []Numa, error) {
	content, err := ioutil.ReadFile(numaTopologyFile)
	if err != nil && os.IsNotExist(err) {
		return false, nil, nil, nil
	} else if err != nil {
		return false, nil, nil, fmt.Errorf("Fail to read numa topology file %s: %v", numaTopologyFile, err)
	}
	var numaData PseudoNUMAData
	if err = json.Unmarshal(content, &numaData); err != nil {
		return false, nil, nil, fmt.Errorf("Could not unmarshal numa topology file %s: %v", numaTopologyFile, err)
	}
	if err := checkVendor(sysVendorFile, numaData.Vendor); err != nil {
		return false, nil, nil, fmt.Errorf("Check vendor failed: %v", err)
	}

	return getTopologyFromPseudoNUMAData(numaData)
}

func checkVendor(file, vendorInTopology string) error {
	content, err := ioutil.ReadFile(file)
	if err != nil {
		return fmt.Errorf("Fail to read vendor file %s: %v", file, err)
	}
	vendorInFile := strings.Replace(string(content), "\n", "", -1)
	if vendorInFile != vendorInTopology {
		return fmt.Errorf("Vendor names are different in topology file(%s) and vendor file(%s)", vendorInTopology, vendorInFile)
	}
	return nil
}

func getTopologyFromPseudoNUMAData(numaData PseudoNUMAData) (bool, []cadvisorapi.Node, []Numa, error) {
	numasOfSocket := make(map[int]sets.Int)
	coresOfNuma := make(map[int]sets.Int)
	threadsOfCore := make(map[int]sets.Int)
	for cpuID, cpuInfo := range numaData.Topology {
		socketID := cpuInfo.SocketID
		numaID := cpuInfo.NumaID
		coreID := cpuInfo.CoreID
		if numasOfSocket[socketID] == nil {
			numasOfSocket[socketID] = make(sets.Int)
		}
		numasOfSocket[socketID].Insert(numaID)
		if coresOfNuma[numaID] == nil {
			coresOfNuma[numaID] = make(sets.Int)
		}
		coresOfNuma[numaID].Insert(coreID)
		if threadsOfCore[coreID] == nil {
			threadsOfCore[coreID] = make(sets.Int)
		}
		threadsOfCore[coreID].Insert(cpuID)
	}
	var cadvisorTopology []cadvisorapi.Node
	var numaTopology []Numa
	for socketID, numaSet := range numasOfSocket {
		var cadvisorCoresInNuma []cadvisorapi.Core
		for _, numa := range numaSet.List() {
			var coresInNuma []Core
			for _, core := range coresOfNuma[numa].List() {
				coresInNuma = append(coresInNuma, Core{
					Id:      core,
					Threads: threadsOfCore[core].List(),
				})
				cadvisorCoresInNuma = append(cadvisorCoresInNuma, cadvisorapi.Core{
					Id:       core,
					Threads:  threadsOfCore[core].List(),
					SocketID: socketID,
				})
			}
			numaTopology = append(numaTopology, Numa{
				Id:    numa,
				Cores: coresInNuma,
			})
			cadvisorTopology = append(cadvisorTopology, cadvisorapi.Node{
				Id:    numa,
				Cores: cadvisorCoresInNuma,
			})
		}
	}
	sort.Slice(numaTopology, func(i, j int) bool {
		return numaTopology[i].Id < numaTopology[j].Id
	})
	sort.Slice(cadvisorTopology, func(i, j int) bool {
		return cadvisorTopology[i].Id < cadvisorTopology[j].Id
	})
	return true, cadvisorTopology, numaTopology, nil
}
