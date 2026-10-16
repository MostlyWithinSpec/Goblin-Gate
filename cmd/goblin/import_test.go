package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPfSenseCLI(t *testing.T) {
	var gotQuery, gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotType = r.URL.Query().Get("devices"), r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"report":[{"section":"nat","item":"DMZ","status":"converted","detail":"every port","risk":true}],
			"interfaces":[{"pfsense":"opt1","pfsense_device":"em2","name":"iot","role":"lan","device":""}],
			"summary":{"converted":1}}`)
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "config.xml")
	os.WriteFile(file, []byte("<pfsense/>"), 0o644)

	var out bytes.Buffer
	if code := runImportPfSense(srv.URL, "tok", []string{file, "--devices", "wan=eth0,opt1=eth2"}, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if gotQuery != `{"opt1":"eth2","wan":"eth0"}` || gotType != "application/xml" || string(gotBody) != "<pfsense/>" {
		t.Errorf("request: devices=%q type=%q body=%q", gotQuery, gotType, gotBody)
	}
	s := out.String()
	for _, want := range []string{"NAT", "DMZ: every port  <-- check this", "NOT ASSIGNED (use --devices opt1=<device>)", "1 converted"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if code := runImportPfSense(srv.URL, "", []string{file, "--devices", "wan"}, io.Discard); code != 2 {
		t.Errorf("bad --devices: exit %d", code)
	}
	if code := runImportPfSense(srv.URL, "", nil, io.Discard); code != 2 {
		t.Errorf("no file: exit %d", code)
	}
}
