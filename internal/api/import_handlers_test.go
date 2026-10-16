package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestImportPfSense(t *testing.T) {
	a := newTestAPI(t)
	data, err := os.ReadFile(filepath.Join("..", "importer", "pfsense", "testdata", "sample.xml"))
	if err != nil {
		t.Fatal(err)
	}
	post := func(body *bytes.Buffer, contentType, query string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(a.srv.URL+"/api/v1/import/pfsense"+query, contentType, body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// raw XML with a device assignment
	devices := url.QueryEscape(`{"opt2":"eth2","igb3":"eth3"}`)
	code, out := post(bytes.NewBuffer(data), "application/xml", "?devices="+devices)
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if len(out["report"].([]any)) < 20 || out["summary"].(map[string]any)["converted"] == nil {
		t.Fatalf("report missing: %v", out)
	}
	if errs, _ := out["validation_errors"].([]any); len(errs) != 0 {
		t.Errorf("validation errors: %v", errs)
	}
	cand, _ := a.store.Candidate()
	if cand.System.Hostname != "edge" || len(cand.Policies) == 0 {
		t.Fatalf("candidate not replaced: %s, %d policies", cand.System.Hostname, len(cand.Policies))
	}
	if run, _ := a.store.Running(); run != nil && run.System.Hostname == "edge" {
		t.Fatal("the import must not commit")
	}

	// as a form upload
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	f, _ := w.CreateFormFile("config", "config.xml")
	f.Write(data)
	w.Close()
	if code, out := post(&form, w.FormDataContentType(), ""); code != 200 {
		t.Fatalf("form upload: %d %v", code, out)
	}

	// not a pfSense file
	if code, out := post(bytes.NewBufferString("<html/>"), "application/xml", ""); code != 400 || errCode(out) != "invalid_import" {
		t.Fatalf("garbage: %d %v", code, out)
	}
	if code, _ := post(bytes.NewBuffer(data), "application/xml", "?devices=nope"); code != 400 {
		t.Fatalf("bad devices: %d", code)
	}
}
