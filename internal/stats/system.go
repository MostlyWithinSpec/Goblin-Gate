package stats

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// System is GET /api/v1/stats/system. On non-Linux hosts every field is 0.
type System struct {
	CPUPercent    float64 `json:"cpu_percent"` // since the previous call (0 on the first)
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"` // total - available
	UptimeSeconds int64   `json:"uptime_seconds"`
	Sessions      int64   `json:"sessions"` // conntrack entries
}

// SystemSampler reads /proc. CPU usage needs two samples, so it remembers
// the previous one. ReadFile is os.ReadFile unless a test replaces it.
type SystemSampler struct {
	ReadFile func(string) ([]byte, error)

	mu              sync.Mutex
	prevIdle, prevT uint64
}

// Sample returns the current numbers; unreadable files leave their fields 0.
func (s *SystemSampler) Sample() System {
	read := s.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	var out System
	if b, err := read("/proc/stat"); err == nil {
		if idle, total, ok := parseCPU(b); ok {
			s.mu.Lock()
			if s.prevT > 0 && total > s.prevT {
				busy := float64((total-s.prevT)-(idle-s.prevIdle)) / float64(total-s.prevT)
				out.CPUPercent = float64(int(busy*1000+0.5)) / 10
			}
			s.prevIdle, s.prevT = idle, total
			s.mu.Unlock()
		}
	}
	if b, err := read("/proc/meminfo"); err == nil {
		total, avail := parseMeminfo(b)
		out.MemTotalBytes = total
		if avail <= total {
			out.MemUsedBytes = total - avail
		}
	}
	if b, err := read("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out.UptimeSeconds = int64(v)
			}
		}
	}
	if b, err := read("/proc/sys/net/netfilter/nf_conntrack_count"); err == nil {
		out.Sessions, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	return out
}

// parseCPU sums the aggregate "cpu" line of /proc/stat: idle includes iowait.
func parseCPU(b []byte) (idle, total uint64, ok bool) {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		for i, v := range f[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			if i >= 8 { // guest time is already counted in user/nice
				break
			}
			total += n
			if i == 3 || i == 4 {
				idle += n
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}

func parseMeminfo(b []byte) (total, avail uint64) {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			avail = n * 1024
		}
	}
	return total, avail
}
