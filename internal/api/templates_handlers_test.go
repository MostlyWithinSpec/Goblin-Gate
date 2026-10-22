package api

import "testing"

func TestTemplatesAPI(t *testing.T) {
	a := newTestAPI(t)
	list := a.expect("GET", "/api/v1/templates", nil, 200)
	if n := len(list["templates"].([]any)); n < 5 {
		t.Fatalf("%d templates", n)
	}

	before, _ := a.store.Candidate()
	body := map[string]any{"values": map[string]string{"devices": "192.168.1.50"}}
	p := a.expect("POST", "/api/v1/templates/bedtime/preview", body, 200)
	if len(p["changes"].([]any)) == 0 || len(p["input_errors"].([]any)) != 0 || len(p["validation_errors"].([]any)) != 0 {
		t.Fatalf("preview %v", p)
	}
	after, _ := a.store.Candidate()
	if len(before.Policies) != len(after.Policies) {
		t.Fatal("preview changed the candidate")
	}

	a.expect("POST", "/api/v1/templates/bedtime/apply", body, 200)
	cand, _ := a.store.Candidate()
	if cand.Policies[0].Name != "bedtime" {
		t.Fatalf("policies %+v", cand.Policies)
	}
	running, _ := a.store.Running()
	if len(running.Policies) != len(before.Policies) {
		t.Fatal("apply committed")
	}

	// the same names again: refused with the input error in the details
	r := a.expect("POST", "/api/v1/templates/bedtime/apply", body, 400)
	if errCode(r) != "template_invalid" {
		t.Fatalf("second apply %v", r)
	}
	p = a.expect("POST", "/api/v1/templates/bedtime/preview", map[string]any{"values": map[string]string{"devices": "x"}}, 200)
	if len(p["input_errors"].([]any)) == 0 {
		t.Fatalf("bad input preview %v", p)
	}
	a.expect("POST", "/api/v1/templates/nope/preview", body, 404)
}
