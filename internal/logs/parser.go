package logs

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrSkip marks a line that is valid but not something we store (another
// program's log prefix, a Suricata event type we ignore).
var ErrSkip = errors.New("logs: line skipped")

// Default log files and the nft log group ulogd2 listens on.
const (
	NftablesLog = "/var/log/ulog/goblin.json"  // ulogd2 JSON output, see packaging/ulogd
	SuricataLog = "/var/log/suricata/eve.json" // Suricata EVE
	NFLogGroup  = 1
)

var prefixRe = regexp.MustCompile(`^GG:(\d+):`) // internal/render/nft LogPrefix

// eveTime is the timestamp layout of Suricata EVE and ulogd2 JSON.
const eveTime = "2006-01-02T15:04:05.999999-0700"

func parseTime(s string) (int64, error) {
	for _, layout := range []string{time.RFC3339Nano, eveTime} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("bad timestamp %q", s)
}

var protoNames = map[int]string{1: "icmp", 6: "tcp", 17: "udp", 58: "ipv6-icmp"}

// ParseNftables parses one line of ulogd2's JSON output for packets logged
// by a Goblin policy (prefix "GG:<policy id>:"). The line does not say
// whether the policy accepted or dropped the packet; Action stays empty and
// the ingester fills it in from the config.
func ParseNftables(line string) (*Event, error) {
	var err error
	var v struct {
		Timestamp string `json:"timestamp"` // local time without a zone
		TimeSec   *int64 `json:"oob.time.sec"`
		Prefix    string `json:"oob.prefix"`
		In        string `json:"oob.in"`
		Out       string `json:"oob.out"`
		Src       string `json:"src_ip"`
		Dst       string `json:"dest_ip"`
		Proto     *int   `json:"ip.protocol"`
		NextHdr   *int   `json:"ip6.nexthdr"` // IPv6 packets carry this instead of ip.protocol
		SrcPort   int    `json:"src_port"`
		DstPort   int    `json:"dest_port"`
		TotLen    int    `json:"ip.totlen"`
		PktLen    int    `json:"raw.pktlen"`
	}
	if err = json.Unmarshal([]byte(line), &v); err != nil {
		return nil, fmt.Errorf("nftables log: %w", err)
	}
	m := prefixRe.FindStringSubmatch(v.Prefix)
	if m == nil {
		return nil, ErrSkip
	}
	// ulogd's "timestamp" has no zone; oob.time.sec is exact.
	var t int64
	if v.TimeSec != nil {
		t = *v.TimeSec
	} else if t, err = parseTime(v.Timestamp); err != nil {
		return nil, fmt.Errorf("nftables log: %w", err)
	}
	e := &Event{
		Time: t, Kind: "traffic", PolicyID: m[1],
		SrcIP: v.Src, DstIP: v.Dst, SrcPort: v.SrcPort, DstPort: v.DstPort,
		InIntf: v.In, OutIntf: v.Out, Bytes: v.TotLen,
	}
	if e.Bytes == 0 {
		e.Bytes = v.PktLen
	}
	if v.Proto == nil {
		v.Proto = v.NextHdr
	}
	if v.Proto != nil {
		if name, ok := protoNames[*v.Proto]; ok {
			e.Proto = name
		} else {
			e.Proto = strconv.Itoa(*v.Proto)
		}
	}
	return e, nil
}

// ParseSuricata parses one Suricata EVE JSON line. alert → kind ips (action
// alert, or drop when Suricata blocked it); dns queries → kind dns; tls →
// traffic with the SNI as message; flow → traffic with bytes. Other event
// types return ErrSkip.
func ParseSuricata(line string) (*Event, error) {
	var v struct {
		Timestamp string `json:"timestamp"`
		EventType string `json:"event_type"`
		InIface   string `json:"in_iface"`
		Src       string `json:"src_ip"`
		SrcPort   int    `json:"src_port"`
		Dst       string `json:"dest_ip"`
		DstPort   int    `json:"dest_port"`
		Proto     string `json:"proto"`
		AppProto  string `json:"app_proto"`
		Alert     struct {
			Action      string `json:"action"` // allowed | blocked
			SignatureID int    `json:"signature_id"`
			Signature   string `json:"signature"`
			Category    string `json:"category"`
			Severity    int    `json:"severity"`
		} `json:"alert"`
		DNS struct {
			Type    string `json:"type"` // query | answer (EVE v2)
			RRName  string `json:"rrname"`
			Queries []struct {
				RRName string `json:"rrname"`
			} `json:"queries"` // EVE v3
		} `json:"dns"`
		TLS struct {
			SNI string `json:"sni"`
		} `json:"tls"`
		Flow struct {
			BytesToServer int `json:"bytes_toserver"`
			BytesToClient int `json:"bytes_toclient"`
		} `json:"flow"`
	}
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return nil, fmt.Errorf("suricata eve: %w", err)
	}
	t, err := parseTime(v.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("suricata eve: %w", err)
	}
	e := &Event{
		Time: t, SrcIP: v.Src, SrcPort: v.SrcPort, DstIP: v.Dst, DstPort: v.DstPort,
		Proto: strings.ToLower(v.Proto), InIntf: v.InIface, App: v.AppProto,
	}
	switch v.EventType {
	case "alert":
		e.Kind, e.Action = "ips", "alert"
		if v.Alert.Action == "blocked" {
			e.Action = "drop"
		}
		e.Signature = v.Alert.Signature
		if v.Alert.SignatureID != 0 {
			e.Signature = fmt.Sprintf("%d: %s", v.Alert.SignatureID, v.Alert.Signature)
		}
		e.Category = v.Alert.Category
		if v.Alert.Severity != 0 {
			e.Severity = strconv.Itoa(v.Alert.Severity)
		}
		e.Message = v.Alert.Signature
	case "dns":
		name := v.DNS.RRName
		if len(v.DNS.Queries) > 0 {
			name = v.DNS.Queries[0].RRName
		}
		if name == "" || v.DNS.Type == "answer" {
			return nil, ErrSkip // count each lookup once, at the query
		}
		e.Kind, e.Action, e.Message = "dns", "allow", name
	case "tls":
		e.Kind, e.Action, e.App = "traffic", "allow", "tls"
		if v.TLS.SNI != "" {
			e.Message = "SNI " + v.TLS.SNI
		}
	case "flow":
		e.Kind, e.Action = "traffic", "allow"
		e.Bytes = v.Flow.BytesToServer + v.Flow.BytesToClient
	default:
		return nil, ErrSkip
	}
	return e, nil
}
