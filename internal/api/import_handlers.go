package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/importer/pfsense"
)

// POST /api/v1/import/pfsense — a pfSense config.xml (raw body or form
// field "config") → the converted config becomes the candidate (nothing is
// committed) and the answer is {report, interfaces, summary,
// validation_errors}. Optional device assignment: query or form field
// "devices", JSON {"wan": "eth0", "opt1": "eth2"} (pfSense interface → device
// of this box); unassigned interfaces keep their pfSense device name until
// assigned (send the file again with "devices").
func (s *Server) importPfSense(w http.ResponseWriter, r *http.Request) {
	var data []byte
	var err error
	devicesJSON := r.URL.Query().Get("devices")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		f, _, ferr := r.FormFile("config")
		if ferr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "upload the config.xml in form field \"config\"", nil)
			return
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, MaxBackupSize+1))
		if v := r.FormValue("devices"); v != "" {
			devicesJSON = v
		}
	} else {
		data, err = io.ReadAll(io.LimitReader(r.Body, MaxBackupSize+1))
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read the upload", err.Error())
		return
	}
	if len(data) > MaxBackupSize {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("a config.xml can be at most %d MB", MaxBackupSize>>20), nil)
		return
	}
	var devices map[string]string
	if devicesJSON != "" {
		if err := json.Unmarshal([]byte(devicesJSON), &devices); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "devices: want JSON like {\"wan\": \"eth0\"}", err.Error())
			return
		}
	}
	// admins and the default WAN/LAN devices come from the running config
	// (a previous import's candidate may hold pfSense device names)
	base, err := s.Store.Running()
	if err != nil {
		if base, err = s.Store.Candidate(); err != nil {
			s.internal(w, "read config", err)
			return
		}
	}
	res, err := pfsense.Import(data, pfsense.Options{Base: base, Devices: devices})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import", err.Error(), nil)
		return
	}
	if err := s.Store.SetCandidate(res.Config); err != nil {
		s.internal(w, "store imported candidate", err)
		return
	}
	s.audit(r, "import_pfsense", s.user(r), map[string]any{"converted": res.Summary[pfsense.Converted],
		"approximated": res.Summary[pfsense.Approximated], "skipped": res.Summary[pfsense.Skipped]})
	writeJSON(w, http.StatusOK, res)
}
