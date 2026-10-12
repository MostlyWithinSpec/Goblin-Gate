package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/version"
)

// MaxBackupSize caps an uploaded backup (POST /api/v1/system/restore).
const MaxBackupSize = 32 << 20

// backupManifest describes a backup archive.
type backupManifest struct {
	Format   int    `json:"format"` // 1
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Created  string `json:"created"`
	Revision int    `json:"revisions"`
}

// GET /api/v1/system/backup → a zip: manifest.json, running.json and every
// revision (history/NNNNNN.json + history/index.json). It contains the admin
// password hashes and every secret in the config: keep it safe.
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	running, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config for backup", err)
		return
	}
	revs, err := s.Store.ListRevisions()
	if err != nil {
		s.internal(w, "list revisions for backup", err)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(append(b, '\n'))
		return err
	}
	err = add("manifest.json", backupManifest{Format: 1, Version: version.Version, Hostname: running.System.Hostname,
		Created: time.Now().UTC().Format(time.RFC3339), Revision: len(revs)})
	if err == nil {
		err = add("running.json", running)
	}
	if err == nil && len(revs) > 0 {
		err = add("history/index.json", revs)
		for _, rev := range revs {
			if err != nil {
				break
			}
			var cfg *config.Config
			if cfg, err = s.Store.Revision(rev.Revision); err == nil {
				err = add(fmt.Sprintf("history/%06d.json", rev.Revision), cfg)
			}
		}
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		s.internal(w, "write backup", err)
		return
	}
	name := fmt.Sprintf("goblin-gate-%s-%s.zip", running.System.Hostname, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	s.audit(r, "backup_downloaded", s.user(r), nil)
	w.Write(buf.Bytes())
}

// POST /api/v1/system/restore (multipart field "backup", or the zip as the
// raw body) → the backup's running config becomes the candidate; review the
// diff and commit it like any other change. Nothing is applied here.
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {

	var data []byte
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		f, _, ferr := r.FormFile("backup")
		if ferr != nil {
			writeError(w, http.StatusBadRequest, "invalid_backup", "send the backup as the form field \"backup\"", ferr.Error())
			return
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, MaxBackupSize+1))
	} else {
		data, err = io.ReadAll(io.LimitReader(r.Body, MaxBackupSize+1))
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup", "could not read the backup", err.Error())
		return
	}
	if len(data) > MaxBackupSize {
		writeError(w, http.StatusRequestEntityTooLarge, "backup_too_large", fmt.Sprintf("a backup can be at most %d MB", MaxBackupSize>>20), nil)
		return
	}
	cfg, err := readBackup(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup", "this is not a Goblin Gate backup", err.Error())
		return
	}
	if errs := config.Validate(cfg); len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "the backup's config does not validate on this version", errs)
		return
	}
	if err := s.Store.SetCandidate(cfg); err != nil {
		s.internal(w, "store restored candidate", err)
		return
	}
	s.audit(r, "backup_restored", s.user(r), map[string]any{"hostname": cfg.System.Hostname})
	writeJSON(w, http.StatusOK, map[string]any{"status": "candidate", "hostname": cfg.System.Hostname,
		"message": "The backup is loaded as the candidate config: review the changes and commit."})
}

// readBackup returns the running config inside a backup zip.
func readBackup(data []byte) (*config.Config, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != "running.json" {
			continue
		}
		if f.UncompressedSize64 > MaxBackupSize {
			return nil, errors.New("running.json is too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return config.Load(io.LimitReader(rc, MaxBackupSize))
	}
	return nil, errors.New("running.json is missing")
}

// GET /api/v1/system/info → version, hostname, kernel, uptime, time and time
// zone, NTP sync. Fields the platform cannot answer are left out.
func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"version": version.Version, "time": time.Now().UTC().Format(time.RFC3339)}
	if h, err := os.Hostname(); err == nil {
		out["hostname"] = h
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		out["kernel"] = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if secs, err := strconv.ParseFloat(f[0], 64); err == nil {
				out["uptime_seconds"] = int64(secs)
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if b, err := exec.CommandContext(ctx, "timedatectl", "show", "-p", "Timezone", "-p", "NTPSynchronized").Output(); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "Timezone":
				out["timezone"] = v
			case "NTPSynchronized":
				out["ntp_synchronized"] = v == "yes"
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- admins ---

type adminInfo struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	HasPassword bool   `json:"has_password"`
}

// GET /api/v1/admins → {admins: [{username, role, has_password}]} (never hashes).
func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	out := []adminInfo{}
	for _, a := range cfg.Admins {
		out = append(out, adminInfo{Username: a.Username, Role: a.Role, HasPassword: a.PasswordHash != ""})
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out})
}

type adminRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// POST /api/v1/admins {username, role, password} → 201 {admin}.
func (s *Server) createAdmin(w http.ResponseWriter, r *http.Request) {
	var req adminRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}
	if msg := checkAdminFields(req.Username, req.Role); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg, nil)
		return
	}
	hash, ok := s.hashFor(w, req.Password)
	if !ok {
		return
	}
	err := s.updateAdmins(r, "admin "+req.Username+" added", func(admins *[]config.Admin) error {
		for _, a := range *admins {
			if a.Username == req.Username {
				return errConflict("an admin named " + req.Username + " already exists")
			}
		}
		*admins = append(*admins, config.Admin{Username: req.Username, Role: req.Role, PasswordHash: hash})
		return nil
	})
	if s.adminError(w, err) {
		return
	}
	s.audit(r, "admin_created", req.Username, map[string]any{"role": req.Role})
	writeJSON(w, http.StatusCreated, map[string]any{"admin": adminInfo{Username: req.Username, Role: req.Role, HasPassword: true}})
}

// PATCH /api/v1/admins/{name} {role?, password?}: change the role or set a new
// password (admins reset other admins' passwords here; no current password).
func (s *Server) updateAdmin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == nil && req.Password == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "give a role, a password or both", nil)
		return
	}
	if req.Role != nil {
		if msg := checkAdminFields(name, *req.Role); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg, nil)
			return
		}
	}
	hash := ""
	if req.Password != nil {
		var ok bool
		if hash, ok = s.hashFor(w, *req.Password); !ok {
			return
		}
	}
	err := s.updateAdmins(r, "admin "+name+" changed", func(admins *[]config.Admin) error {
		i := adminIndex(*admins, name)
		if i < 0 {
			return errNotFound
		}
		if req.Role != nil {
			(*admins)[i].Role = *req.Role
		}
		if hash != "" {
			(*admins)[i].PasswordHash = hash
		}
		return lastAdminCheck(*admins)
	})
	if s.adminError(w, err) {
		return
	}
	if s.Authn != nil {
		s.Authn.Sessions.DeleteUser(name) // new role or password: log in again
	}
	s.audit(r, "admin_changed", name, map[string]any{"role": req.Role, "password_reset": req.Password != nil})
	writeJSON(w, http.StatusOK, map[string]any{"status": "changed"})
}

// DELETE /api/v1/admins/{name}: not yourself, not the last admin.
func (s *Server) deleteAdmin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if id, ok := IdentityFrom(r); ok && id.User == name {
		writeError(w, http.StatusConflict, "self_delete", "you cannot delete your own account; another admin can", nil)
		return
	}
	err := s.updateAdmins(r, "admin "+name+" deleted", func(admins *[]config.Admin) error {
		i := adminIndex(*admins, name)
		if i < 0 {
			return errNotFound
		}
		*admins = append((*admins)[:i], (*admins)[i+1:]...)
		return lastAdminCheck(*admins)
	})
	if s.adminError(w, err) {
		return
	}
	if s.Authn != nil {
		s.Authn.Sessions.DeleteUser(name)
	}
	s.audit(r, "admin_deleted", name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

var errNotFound = errors.New("no such admin")

type errConflict string

func (e errConflict) Error() string { return string(e) }

func adminIndex(admins []config.Admin, name string) int {
	for i, a := range admins {
		if a.Username == name {
			return i
		}
	}
	return -1
}

// lastAdminCheck keeps at least one admin who can log in with full rights.
func lastAdminCheck(admins []config.Admin) error {
	for _, a := range admins {
		if a.Role == "admin" && a.PasswordHash != "" {
			return nil
		}
	}
	return errConflict("at least one admin with the admin role and a password must remain")
}

func checkAdminFields(name, role string) string {
	if !config.ValidName(name) {
		return "username must be 1-63 letters, digits, \".\", \"_\" or \"-\""
	}
	if role != "admin" && role != "readonly" {
		return "role must be admin or readonly"
	}
	return ""
}

func (s *Server) hashFor(w http.ResponseWriter, password string) (string, bool) {
	if err := auth.CheckPassword(password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), []map[string]string{{"path": "password", "message": err.Error()}})
		return "", false
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.internal(w, "hash password", err)
		return "", false
	}
	return hash, true
}

func (s *Server) adminError(w http.ResponseWriter, err error) bool {
	var conflict errConflict
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "admin_not_found", err.Error(), nil)
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, "admin_conflict", err.Error(), nil)
	default:
		s.internal(w, "update admins", err)
	}
	return true
}

// updateAdmins changes the admin list right away: admins are the management
// plane (nothing to render), like a password change. The running config gets
// a new revision and the candidate the same change, so a later commit keeps it.
func (s *Server) updateAdmins(r *http.Request, comment string, change func(*[]config.Admin) error) error {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	running, err := s.Store.Running()
	if err != nil {
		return err
	}
	if err := change(&running.Admins); err != nil {
		return err
	}
	cand, err := s.Store.Candidate()
	if err != nil {
		return err
	}
	if _, err := s.Store.SaveRevision(running, s.user(r), comment); err != nil {
		return err
	}
	if err := change(&cand.Admins); err != nil {
		s.Logger.Warn("admins: the candidate could not take the change", "error", err)
		return nil
	}
	if err := s.Store.SetCandidate(cand); err != nil {
		s.Logger.Warn("admins: could not update the candidate", "error", err)
	}
	return nil
}
