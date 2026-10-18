package devices

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"strings"
)

// randomizedVendor is reported for locally-administered MACs, which carry no
// IEEE-registered vendor.
const randomizedVendor = "Private (randomized MAC)"

var ouiTable map[string]string // first 3 octets (hex, lower) -> vendor

func init() {
	f, err := ouiGz.Open("oui.txt.gz")
	if err != nil {
		panic(fmt.Sprintf("devices: embedded OUI file: %v", err))
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		panic(fmt.Sprintf("devices: OUI gzip: %v", err))
	}
	defer zr.Close()
	tab := map[string]string{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		if len(key) != 6 {
			continue
		}
		tab[key] = parts[1]
	}
	ouiTable = tab
}

// vendorOf looks up the vendor of a MAC. A locally-administered address
// (second hex digit 2, 6, a or e) is reported as randomized instead.
func vendorOf(mac string) (vendor string, randomized bool) {
	if len(mac) < 2 {
		return "", false
	}
	switch mac[1] {
	case '2', '6', 'a', 'e':
		return randomizedVendor, true
	}
	if v, ok := ouiTable[mac[:6]]; ok {
		return v, false
	}
	return "Unknown", false
}
