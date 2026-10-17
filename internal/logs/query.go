package logs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Query is the body of POST /api/v1/stats/query. Field names and values are
// checked against whitelists; user values only ever reach SQL as
// placeholders.
type Query struct {
	Source  string            `json:"source"`   // events (default) | rollup
	Metric  string            `json:"metric"`   // count (default) | bytes
	GroupBy []string          `json:"group_by"` // fields, see GroupFields
	Filters map[string]string `json:"filters"`  // field -> exact value
	From    string            `json:"from"`     // default -1h; see ParseTime
	To      string            `json:"to"`       // default now
	Bucket  string            `json:"bucket"`   // 1m | 5m | 1h | none (default)
	Limit   int               `json:"limit"`    // default 100, max MaxQueryRows
}

// Result is a query answer: one row per group (and bucket), values in
// Columns order. A bucketed query starts with a "time" column (Unix seconds
// of the bucket start); the metric is the last column.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// MaxQueryRows caps Query.Limit and ListEvents' limit.
const MaxQueryRows = 10000

// Fields that may be grouped or filtered on, per source.
var (
	eventFields  = []string{"kind", "action", "policy_id", "src_ip", "src_port", "dst_ip", "dst_port", "proto", "in_intf", "out_intf", "app", "category", "signature", "severity"}
	rollupFields = []string{"kind", "action", "policy_id", "in_intf", "out_intf"}
	buckets      = map[string]int64{"1m": 60, "5m": 300, "1h": 3600}
)

// GroupFields returns the fields a source can group and filter on.
func GroupFields(source string) []string {
	if source == "rollup" {
		return rollupFields
	}
	return eventFields
}

func allowed(source, field string) bool {
	for _, f := range GroupFields(source) {
		if f == field {
			return true
		}
	}
	return false
}

var relTime = regexp.MustCompile(`^-(\d{1,6})([smhd])$`)

// ParseTime accepts "now", a relative time like "-15m", "-1h", "-7d",
// RFC 3339, or Unix seconds; it returns Unix seconds.
func ParseTime(s string, now time.Time) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "now" {
		return now.Unix(), nil
	}
	if m := relTime.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit).Unix(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
		return n, nil
	}
	return 0, fmt.Errorf("invalid time %q: want now, -15m/-1h/-7d, RFC 3339 or Unix seconds", s)
}

// Build checks q and returns the SQL, its arguments and the result columns.
func (q Query) Build(now time.Time) (string, []any, []string, error) {
	source := q.Source
	if source == "" {
		source = "events"
	}
	table := map[string]string{"events": "events", "rollup": "rollup_1m"}[source]
	if table == "" {
		return "", nil, nil, fmt.Errorf("source must be events or rollup, not %q", q.Source)
	}
	metric := q.Metric
	if metric == "" {
		metric = "count"
	}
	var agg string
	switch {
	case metric == "count" && source == "events":
		agg = "COUNT(*)"
	case metric == "count":
		agg = "COALESCE(SUM(count), 0)"
	case metric == "bytes":
		agg = "COALESCE(SUM(bytes), 0)"
	default:
		return "", nil, nil, fmt.Errorf("metric must be count or bytes, not %q", q.Metric)
	}
	from, err := ParseTime(orDefault(q.From, "-1h"), now)
	if err != nil {
		return "", nil, nil, fmt.Errorf("from: %w", err)
	}
	to, err := ParseTime(q.To, now)
	if err != nil {
		return "", nil, nil, fmt.Errorf("to: %w", err)
	}
	if from > to {
		return "", nil, nil, errors.New("from is after to")
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = 100
	case limit < 0 || limit > MaxQueryRows:
		return "", nil, nil, fmt.Errorf("limit must be between 1 and %d", MaxQueryRows)
	}

	var cols, sel, group []string
	if q.Bucket != "" && q.Bucket != "none" {
		size, ok := buckets[q.Bucket]
		if !ok {
			return "", nil, nil, fmt.Errorf("bucket must be 1m, 5m, 1h or none, not %q", q.Bucket)
		}
		b := fmt.Sprintf("(time - time %% %d)", size) // size comes from the whitelist above
		cols = append(cols, "time")
		sel = append(sel, b+" AS bucket")
		group = append(group, "bucket")
	}
	seen := map[string]bool{}
	for _, f := range q.GroupBy {
		if !allowed(source, f) {
			return "", nil, nil, fmt.Errorf("cannot group %s by %q", source, f)
		}
		if seen[f] {
			return "", nil, nil, fmt.Errorf("group_by lists %q twice", f)
		}
		seen[f] = true
		cols = append(cols, f)
		sel = append(sel, f)
		group = append(group, f)
	}
	cols = append(cols, metric)
	sel = append(sel, agg+" AS value")

	where := []string{"time >= ?", "time < ?"}
	args := []any{from, to + 1}
	for _, f := range sortedKeys(q.Filters) {
		if !allowed(source, f) {
			return "", nil, nil, fmt.Errorf("cannot filter %s on %q", source, f)
		}
		where = append(where, f+" = ?")
		args = append(args, q.Filters[f])
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s WHERE %s", strings.Join(sel, ", "), table, strings.Join(where, " AND "))
	if len(group) > 0 {
		fmt.Fprintf(&b, " GROUP BY %s", strings.Join(group, ", "))
	}
	// biggest first (per bucket), ties broken by the group columns so the
	// order is stable
	order := []string{"value DESC"}
	if len(cols) > 0 && cols[0] == "time" {
		order = []string{"bucket ASC", "value DESC"}
	}
	for _, f := range q.GroupBy {
		order = append(order, f+" ASC")
	}
	fmt.Fprintf(&b, " ORDER BY %s", strings.Join(order, ", "))
	b.WriteString(" LIMIT ?")
	args = append(args, limit)
	return b.String(), args, cols, nil
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Query runs a stats query.
func (s *Store) Query(ctx context.Context, q Query, now time.Time) (Result, error) {
	query, args, cols, err := q.Build(now)
	if err != nil {
		return Result{}, &QueryError{err}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Result{}, fmt.Errorf("stats query: %w", err)
	}
	defer rows.Close()
	res := Result{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, fmt.Errorf("stats query: %w", err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		res.Rows = append(res.Rows, vals)
	}
	return res, rows.Err()
}

// QueryError marks a bad query (as opposed to a database failure).
type QueryError struct{ Err error }

func (e *QueryError) Error() string { return e.Err.Error() }
func (e *QueryError) Unwrap() error { return e.Err }

// EventFilter selects events for ListEvents.
type EventFilter struct {
	Kind     string // exact
	Text     string // substring of message, ips, signature, app, category, policy_id
	From, To int64  // Unix seconds, inclusive; 0 = unbounded
	Before   int64  // cursor: only ids below this; 0 = newest
	After    int64  // only ids above this (new events since a reader's last id); 0 = all
	Limit    int    // default 100, max MaxQueryRows
	// Fields are exact matches on event fields (GroupFields("events"),
	// e.g. "src_ip": "192.168.1.5"); other keys are an error.
	Fields map[string]string
}

// StoredEvent is an Event with its row id (the paging cursor).
type StoredEvent struct {
	ID int64 `json:"id"`
	Event
}

// ListEvents returns events newest first and the cursor for the next page
// (0 when there are no more).
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]StoredEvent, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, MaxQueryRows)
	where := []string{"1=1"}
	var args []any
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.From > 0 {
		where = append(where, "time >= ?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		where = append(where, "time <= ?")
		args = append(args, f.To)
	}
	if f.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, f.Before)
	}
	if f.After > 0 {
		where = append(where, "id > ?")
		args = append(args, f.After)
	}
	for _, k := range sortedKeys(f.Fields) {
		if !allowed("events", k) {
			return nil, 0, fmt.Errorf("list events: cannot filter on %q", k)
		}
		where = append(where, k+" = ?") // k is from the whitelist
		args = append(args, f.Fields[k])
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		var ors []string
		for _, c := range []string{"message", "src_ip", "dst_ip", "signature", "app", "category", "policy_id"} {
			ors = append(ors, c+` LIKE ? ESCAPE '\'`)
			args = append(args, like)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT id, time, kind, action, policy_id, src_ip, src_port, dst_ip, dst_port, proto,
		in_intf, out_intf, bytes, app, category, signature, severity, message
		FROM events WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := []StoredEvent{}
	for rows.Next() {
		var e StoredEvent
		var policy, src, dst, proto, in, outIf, app, cat, sig, sev, msg sql.NullString
		var sport, dport, bytes sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Time, &e.Kind, &e.Action, &policy, &src, &sport, &dst, &dport, &proto,
			&in, &outIf, &bytes, &app, &cat, &sig, &sev, &msg); err != nil {
			return nil, 0, fmt.Errorf("list events: %w", err)
		}
		e.PolicyID, e.SrcIP, e.DstIP, e.Proto = policy.String, src.String, dst.String, proto.String
		e.InIntf, e.OutIntf, e.App, e.Category = in.String, outIf.String, app.String, cat.String
		e.Signature, e.Severity, e.Message = sig.String, sev.String, msg.String
		e.SrcPort, e.DstPort, e.Bytes = int(sport.Int64), int(dport.Int64), int(bytes.Int64)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	var next int64
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}
