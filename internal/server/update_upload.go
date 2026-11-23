package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

// Upload an update file through the web interface (ticket 221, Q154). In
// offline mode a parent brings a release to the box as a file. The box checks
// the file (format, signature, version, architecture; internal/update), keeps
// it, asks for a yes and then hands it to the host script through the settings
// file, with fixed keys and nothing that is run or interpolated. The host
// script, the only installer, checks the file again, saves the data, starts the
// new version and goes back if it does not answer. The container has no access
// to Docker.
//
// One file waits at most an hour; an installation that reports nothing for two
// hours is given up. One file at a time, for the whole box.

const (
	uploadPagePath = "/settings/connections/update"
	installPath    = uploadPagePath + "/install"
	discardPath    = uploadPagePath + "/discard"

	uploadTTL    = time.Hour
	installStall = 2 * time.Hour

	// headroom is room the installation needs besides two copies of the file (the one the host script keeps
	// and the new image once loaded): the data backup and Docker's own bookkeeping.
	headroom = 8 << 20
	mib      = 1 << 20
)

// uploadOverTLS reports whether a request came over HTTPS; uploads are taken
// only over it, so a file never travels in the clear.
func uploadOverTLS(r *http.Request) bool { return r.TLS != nil }

// uploadsPossible reports whether a host script that can install an uploaded
// file runs beside the box.
func (s *server) uploadsPossible() bool { return s.updateDir != "" && s.updateInstall }

// hostStaysOffline reports whether the host script is told to contact nothing
// (offline mode, or the update check switched off): only then does a file make
// sense, and the nightly check cannot be bypassed.
func (s *server) hostStaysOffline() bool { return !s.gate.Allowed(outbound.Update) }

func (s *server) pendingPath() string { return filepath.Join(s.updateDir, update.PendingFile) }

// freeBytes is the room left on the data volume.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil // #nosec G115 -- sizes of a file system
}

// needBytes is the room a file of this size needs on the box.
func needBytes(size int64) uint64 {
	if size < 0 {
		return headroom
	}
	return 2*uint64(size) + headroom // #nosec G115 -- checked non-negative above
}

func megabytesUp(n uint64) uint64 { return (n + mib - 1) / mib }

// ---- the box's side of the state

// settleUpload brings the stored upload and the data volume in line with what
// happened: a waiting file that nobody answered is deleted, an installation the
// host script reported the end of is closed, one that reports nothing is given
// up, and what an interrupted upload left is removed.
func (s *server) settleUpload() {
	if s.updateDir == "" {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.settleLocked()
}

func (s *server) settleLocked() { s.settle(false) }

// settleAtStart is settleUpload for a box that has just started: nothing is
// being written, so every leftover of an interrupted upload goes.
func (s *server) settleAtStart() {
	if s.updateDir == "" {
		return
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.settle(true)
}

func (s *server) settle(atStart bool) {
	u, ok, err := s.db.UpdateUpload()
	if err != nil {
		s.logger.Error("update upload", "err", err)
		return
	}
	s.removeStrays(ok, atStart)
	if !ok {
		return
	}
	switch u.State {
	case store.UploadReady:
		if s.now().Sub(u.UploadedAt) >= uploadTTL {
			s.dropUpload()
		}
	case store.UploadInstalling:
		st := update.ReadStatus(s.updateDir)
		if finished(st, u) {
			if st.State == update.Updated {
				if err := s.db.SetInstalledRelease(u.Version, u.Released); err != nil {
					s.logger.Error("note installed release", "err", err)
				}
				s.noteUpdated()
			}
			s.dropUpload()
		} else if s.now().Sub(u.StartedAt) >= installStall {
			s.logger.Warn("the host script reported nothing for the installation of an uploaded file; giving it up", "version", u.Version)
			s.dropUpload()
		}
	}
}

// finished reports whether the status is the end of the installation u: a
// final report about its version, made after it was started.
func finished(st update.Status, u store.UpdateUpload) bool {
	t, err := time.Parse(time.RFC3339, st.Time)
	if err != nil || t.Before(u.StartedAt) {
		return false
	}
	switch st.State {
	case update.Updated:
		return st.Version == u.Version
	case update.RolledBack, update.Rejected, update.Failed:
		return st.Target == u.Version
	}
	return false
}

// dropUpload forgets the upload and deletes its file; the settings file loses the request.
func (s *server) dropUpload() {
	_ = os.Remove(s.pendingPath())
	if err := s.db.ClearUpdateUpload(); err != nil {
		s.logger.Error("clear update upload", "err", err)
	}
	s.syncUpdateSettings()
}

// removeStrays deletes the leftovers of an interrupted upload, and the kept
// file when no upload is stored for it. A temporary file written within the
// last minute may belong to an upload that is still coming in, unless the box
// has just started.
func (s *server) removeStrays(kept, atStart bool) {
	entries, err := os.ReadDir(s.updateDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".update-upload-") {
			if info, err := e.Info(); err == nil && !atStart && time.Since(info.ModTime()) < time.Minute {
				continue
			}
			_ = os.Remove(filepath.Join(s.updateDir, e.Name()))
		}
	}
	if !kept {
		_ = os.Remove(s.pendingPath())
	}
}

// fileSHA256 is the SHA-256 of a file, in hex.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- receiving a file

// uploadFailure is a file the box did not take, or a request it could not serve.
type uploadFailure struct {
	status int
	code   string // the key of the text under updf.error., and the code of the API
	args   []string
	err    error
}

func (f *uploadFailure) Error() string { return f.code }

// cleanFileName is the name the browser sent, as a plain name to show: no path, no control characters.
func cleanFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if utf8.RuneCountInString(name) > 120 {
		name = string([]rune(name)[:120])
	}
	return strings.TrimSpace(name)
}

func (s *server) maxUpdateSize() int64 {
	if s.updateMax > 0 {
		return s.updateMax
	}
	return update.MaxFileSize
}

func (s *server) free(dir string) (uint64, error) {
	if s.freeSpace != nil {
		return s.freeSpace(dir)
	}
	return freeBytes(dir)
}

func (s *server) verifier() (update.Verifier, error) {
	if s.updateVerifier != nil {
		return s.updateVerifier, nil
	}
	v, err := update.DefaultVerifier()
	if err != nil {
		return nil, err
	}
	return v, nil
}

// receiveUpload takes the file of a multipart request, checks it and keeps it
// waiting for confirmation. It is the one place that both the page and the API
// use.
func (s *server) receiveUpload(w http.ResponseWriter, r *http.Request) (store.UpdateUpload, *uploadFailure) {
	// The lock is held while the box looks at its state and again while it keeps the file, not while the
	// file comes in: that takes minutes on a slow network, and everyone else's page would wait.
	s.uploadMu.Lock()
	s.settleLocked()
	var early *uploadFailure
	switch {
	case !uploadOverTLS(r):
		early = &uploadFailure{status: http.StatusForbidden, code: "https"}
	case !s.uploadsPossible() || !s.hostStaysOffline() || s.rollbackFailed():
		early = &uploadFailure{status: http.StatusConflict, code: "unavailable"}
	}
	if cur, ok, _ := s.db.UpdateUpload(); early == nil && ok && cur.State == store.UploadInstalling {
		early = &uploadFailure{status: http.StatusConflict, code: "busy"}
	}
	s.uploadMu.Unlock()
	if early != nil {
		return store.UpdateUpload{}, early
	}
	max := s.maxUpdateSize()
	free, err := s.free(s.updateDir)
	if err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	if r.ContentLength > 0 && free < needBytes(r.ContentLength) {
		return store.UpdateUpload{}, noRoom(free, needBytes(r.ContentLength))
	}
	r.Body = http.MaxBytesReader(w, r.Body, max+mib) // the form's own framing is no part of the file
	mr, err := r.MultipartReader()
	if err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusUnprocessableEntity, code: "no_file"}
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return store.UpdateUpload{}, &uploadFailure{status: http.StatusUnprocessableEntity, code: "no_file"}
		}
		if err != nil {
			return store.UpdateUpload{}, interrupted(err)
		}
		if part.FormName() != "update_file" {
			continue
		}
		name := cleanFileName(part.FileName())
		if name == "" {
			return store.UpdateUpload{}, &uploadFailure{status: http.StatusUnprocessableEntity, code: "no_file"}
		}
		if !strings.HasSuffix(strings.ToLower(name), update.FileExt) {
			return store.UpdateUpload{}, &uploadFailure{status: http.StatusUnprocessableEntity, code: "not_update_name"}
		}
		return s.keepUpload(part, name, max)
	}
}

func noRoom(free, need uint64) *uploadFailure {
	return &uploadFailure{status: http.StatusUnprocessableEntity, code: "no_room", args: []string{"free", fmt.Sprint(free / mib), "need", fmt.Sprint(megabytesUp(need))}}
}

func interrupted(err error) *uploadFailure {
	return &uploadFailure{status: http.StatusBadRequest, code: "interrupted", err: err}
}

// keepUpload writes the part to a temporary file, checks it and, if it is a good update file, keeps it.
func (s *server) keepUpload(part io.Reader, name string, max int64) (store.UpdateUpload, *uploadFailure) {
	tmp, err := os.CreateTemp(s.updateDir, ".update-upload-*")
	if err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, io.LimitReader(part, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig) || n > max:
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusUnprocessableEntity, code: "too_big_server", args: []string{"name", name, "max", fmt.Sprint(max / mib)}}
	case err != nil:
		return store.UpdateUpload{}, interrupted(err)
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if free, err := s.free(s.updateDir); err == nil && n >= 0 {
		have := free + uint64(n) // #nosec G115 -- checked non-negative above; the file itself is on the disk already
		if have < needBytes(n) {
			return store.UpdateUpload{}, noRoom(have, needBytes(n))
		}
	}
	v, err := s.verifier()
	if err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	running := update.ReadStatus(s.updateDir).Version
	info, err := update.Inspect(tmpName, update.Check{Arch: s.updateArch, Running: running, Verify: v})
	var refusal *update.Refusal
	switch {
	case errors.As(err, &refusal):
		s.logger.Info("update file refused", "reason", refusal.Code, "err", refusal.Err)
		return store.UpdateUpload{}, refusalFailure(refusal, running, s.updateArch)
	case err != nil:
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	if err := os.Rename(tmpName, s.pendingPath()); err != nil {
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	keep = true
	u := store.UpdateUpload{Version: info.Version, FromVersion: running, Arch: info.Arch, FileName: name, Size: info.Size, SHA256: info.SHA256, Released: info.Released, Signer: info.Signer, UploadedAt: s.now()}
	if err := s.db.SaveUpdateUpload(u); err != nil {
		_ = os.Remove(s.pendingPath())
		if errors.Is(err, store.ErrUploadInstalling) {
			return store.UpdateUpload{}, &uploadFailure{status: http.StatusConflict, code: "busy"}
		}
		return store.UpdateUpload{}, &uploadFailure{status: http.StatusInternalServerError, code: "server", err: err}
	}
	u.State = store.UploadReady
	return u, nil
}

// refusalFailure is the field error for a file the check refused.
func refusalFailure(r *update.Refusal, running, boxArch string) *uploadFailure {
	f := &uploadFailure{status: http.StatusUnprocessableEntity, code: r.Code}
	switch r.Code {
	case update.RefuseArch:
		f.args = []string{"version", r.Version, "box_arch", boxArch, "file_arch", r.Arch}
	case update.RefuseOlder:
		f.args = []string{"file", r.Version, "running", running}
		if running == "" {
			f.code = "not_newer_unknown"
		}
	case update.RefusePrerelease:
		f.args = []string{"version", r.Version}
	case update.RefuseNotUpdate:
		f.code = update.RefuseDamaged // the same words: damaged, or not an update file
	}
	return f
}

// text is the failure in words, in the language of the page.
func (s *server) failureText(lang string, f *uploadFailure) string {
	args := f.args
	if f.code == "wrong_arch" {
		args = append([]string(nil), args...)
		for i := 0; i+1 < len(args); i += 2 {
			if args[i] == "file_arch" {
				args[i+1] = s.i18n.T(lang, "updf.arch."+args[i+1])
			}
		}
	}
	return s.i18n.Format(lang, i18n.Msg("updf.error."+f.code, args...))
}

// ---- the page

// runRow is one step of a run list, with its state in words.
type runRow struct {
	State string // done, busy, wait, fail
	Name  string
	Text  string
}

type uploadNotice struct {
	Kind        string // "rolled_back" or "failed" (a refusal before anything changed)
	Lead, Text  string
	DismissBack string
	Run         []runRow
}

// stepOfReason is the step an installation failed at, by the reason the script gave.
var stepOfReason = map[string]string{
	"signature_invalid": "verify", "file_invalid": "verify", "verifier_missing": "verify",
	"backup_failed": "backup",
	"load_failed":   "start", "start_failed": "start",
	"health_check_failed": "health",
}

// installedFact is the "Installed" fact: what runs, and when it came to the box.
func (s *server) installedFact(r *http.Request, p principal, st update.Status, box store.OutboundBox) string {
	lang := s.langOf(r)
	if st.Version == "" {
		if s.updateDir == "" {
			return s.i18n.T(lang, "updf.installed.unknown_nohost")
		}
		return s.i18n.T(lang, "updf.installed.unknown")
	}
	if box.InstalledVersion == st.Version && box.InstalledReleased != "" && !box.UpdatedAt.IsZero() {
		rel, err := time.Parse("2006-01-02", box.InstalledReleased)
		if err == nil {
			at := box.UpdatedAt.In(p.location())
			return s.i18n.Format(lang, i18n.Msg("updf.installed.full", "version", st.Version, "released", datefmt.Date(p.household.DateFormat, rel), "date", datefmt.Date(p.household.DateFormat, at), "time", datefmt.Clock(p.household.TimeFormat, at)))
		}
	}
	return s.i18n.Format(lang, i18n.Msg("updf.installed", "version", st.Version, "date", datefmt.Date(p.household.DateFormat, box.UpdatedAt.In(p.location()))))
}

func archLong(s *server, lang, arch string) string { return s.i18n.T(lang, "updf.arch."+arch) }

// rollbackFailed reports whether the last report says the box could neither
// install nor go back: it needs a person at the box, so no form is offered.
func (s *server) rollbackFailed() bool {
	st := update.ReadStatus(s.updateDir)
	return st.State == update.Failed && st.Reason == "rollback_failed"
}

// renderUploadPage shows the page of the upload form, or the state that stands
// in for it. Status, and an error at the field, come from a refused upload.
func (s *server) renderUploadPage(w http.ResponseWriter, r *http.Request, p principal, status int, fail *uploadFailure) {
	lang := s.langOf(r)
	box := s.outboundBox()
	s.noteUpdated()
	box = s.outboundBox()
	st := update.ReadStatus(s.updateDir)
	arch := s.updateArch
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"Installed": s.installedFact(r, p, st, box), "BoxArch": archLong(s, lang, arch), "Arch": arch,
		"Guide": s.i18n.T(lang, guideUpdateAnchor), "ReleasesURL": "https://github.com/hejmo-home/hejmo/releases",
		"MaxMB": s.maxUpdateSize() / mib, "MaxBytes": s.maxUpdateSize(),
		// The name of the file the box needs, with the version left for the parent to read off the release page.
		"FileHint": template.HTML("hejmo-<i>" + html.EscapeString(s.i18n.T(lang, "updf.s1.version")) + "</i>-" + html.EscapeString(arch) + update.FileExt), // #nosec G203 -- every part is escaped
	}
	switch {
	case s.updateDir == "":
		data["State"] = "nohost"
	case !s.updateInstall:
		data["State"] = "oldhost"
	case !s.hostStaysOffline():
		data["State"] = "online"
	case s.rollbackFailed():
		data["State"] = "rollback_failed"
		data["Installed"] = s.i18n.T(lang, "updf.installed.unknown")
		data["Failed"] = s.rollbackFailedNotice(r, p, st)
	case !uploadOverTLS(r):
		data["State"] = "http"
		data["HTTPSURL"] = s.httpsOrigin(r) + uploadPagePath
		data["HTTPSHost"] = s.httpsOrigin(r)
	default:
		data["State"] = "form"
		v := s.updateView(p.household)
		if v.Notice && status < http.StatusBadRequest {
			switch v.State {
			case update.Updated:
				data["Updated"] = v.Version
				data["NotesURL"] = v.NotesURL
				if err := s.db.AckUpdate(p.household.ID, v.Time); err != nil { // shown once
					s.logger.Error("ack update", "err", err)
				}
			case update.RolledBack, update.Failed, update.Rejected:
				if n := s.rollbackNotice(r, p, v.Status, st); n != nil {
					data["Notice"] = n
				}
			}
		}
	}
	if fail != nil {
		data["ErrorField"] = "update_file"
		data["ErrorText"] = s.failureText(lang, fail)
	}
	s.render(w, r, status, "update_upload.html", data)
}

// rollbackNotice is the notice after an installation that did not work, with how it went.
func (s *server) rollbackNotice(r *http.Request, p principal, v update.Status, cur update.Status) *uploadNotice {
	step, known := stepOfReason[v.Reason]
	if !known || v.Target == "" {
		return nil // a nightly update that failed: the home page tells
	}
	lang := s.langOf(r)
	when := s.i18n.T(lang, "updf.when.before")
	if t, err := time.Parse(time.RFC3339, v.Backup); err == nil {
		when = s.i18n.Format(lang, i18n.Msg("updf.when.at", "time", datefmt.Clock(p.household.TimeFormat, t.In(p.location()))))
	}
	version := v.Version
	if version == "" {
		version = cur.Version
	}
	n := &uploadNotice{Kind: string(v.State), DismissBack: uploadPagePath}
	n.Lead = s.i18n.Format(lang, i18n.Msg("updf.fail."+v.Reason+".lead", "target", v.Target))
	n.Text = s.i18n.Format(lang, i18n.Msg("updf.fail."+v.Reason+".text", "version", version, "when", when))
	n.Run = s.runList(lang, step, v.Reason, version, v.Backup, p)
	return n
}

// runList is the list of steps up to the one that failed.
func (s *server) runList(lang, failedAt, reason, version, backup string, p principal) []runRow {
	var rows []runRow
	for _, step := range update.Steps {
		row := runRow{Name: s.i18n.T(lang, "updf.step."+step)}
		switch {
		case step == failedAt:
			row.State = "fail"
			row.Text = s.i18n.Format(lang, i18n.Msg("updf.run.fail."+reason, "version", version))
		default:
			row.State = "done"
			row.Text = s.i18n.T(lang, "updf.run.done")
			if step == "backup" {
				if t, err := time.Parse(time.RFC3339, backup); err == nil {
					row.Text = s.i18n.Format(lang, i18n.Msg("updf.run.done_at", "time", datefmt.ClockSeconds(p.household.TimeFormat, t.In(p.location()))))
				}
			}
		}
		rows = append(rows, row)
		if step == failedAt {
			break
		}
	}
	return rows
}

func (s *server) rollbackFailedNotice(r *http.Request, p principal, st update.Status) map[string]string {
	lang := s.langOf(r)
	when := s.i18n.T(lang, "updf.when.before")
	if t, err := time.Parse(time.RFC3339, st.Backup); err == nil {
		when = s.i18n.Format(lang, i18n.Msg("updf.when.at", "time", datefmt.Clock(p.household.TimeFormat, t.In(p.location()))))
	}
	version := st.Version
	if version == "" {
		version = s.i18n.T(lang, "updf.version.before")
	}
	return map[string]string{
		"Lead": s.i18n.Format(lang, i18n.Msg("updf.rollback_failed.lead", "version", version)),
		"Text": s.i18n.Format(lang, i18n.Msg("updf.rollback_failed.text", "when", when, "path", "/opt/hejmo/state/pre-update.db")),
	}
}

func (s *server) handleUpdatePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.settleUpload()
	if s.updateDir != "" && s.updateInstall {
		if _, has, _ := s.db.UpdateUpload(); has {
			http.Redirect(w, r, installPath, http.StatusSeeOther)
			return
		}
	}
	s.renderUploadPage(w, r, p, http.StatusOK, nil)
}

func (s *server) handleUpdateUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_, fail := s.receiveUpload(w, r)
	if fail == nil {
		http.Redirect(w, r, installPath, http.StatusSeeOther)
		return
	}
	switch fail.code {
	case "server":
		s.fail(w, "update upload", fail.err)
	case "busy":
		s.renderInstallPage(w, r, p, http.StatusConflict)
	default:
		s.renderUploadPage(w, r, p, fail.status, fail)
	}
}

// fileMegabytes is the size of a file in whole megabytes, at least one.
func fileMegabytes(n int64) int64 {
	if n <= 0 {
		return 1
	}
	return (n + mib - 1) / mib
}

// ---- confirm, progress, discard

// installingRows are the steps of an installation in progress, by where the script is.
func (s *server) installingRows(lang string, st update.Status, u store.UpdateUpload, p principal) []runRow {
	at := 0 // before the script reported: it checks the signature first
	for i, step := range update.Steps {
		if step == installStep(st, u) {
			at = i
		}
	}
	var rows []runRow
	for i, step := range update.Steps {
		row := runRow{Name: s.i18n.T(lang, "updf.step."+step)}
		switch {
		case i < at:
			row.State, row.Text = "done", s.i18n.T(lang, "updf.run.done")
			switch step {
			case "verify":
				row.Text = s.i18n.T(lang, "updf.run.done_verify")
			case "backup":
				if t, err := time.Parse(time.RFC3339, st.Backup); err == nil {
					row.Text = s.i18n.Format(lang, i18n.Msg("updf.run.done_at", "time", datefmt.ClockSeconds(p.household.TimeFormat, t.In(p.location()))))
				}
			}
		case i == at:
			row.State, row.Text = "busy", s.i18n.T(lang, "updf.run.busy")
			switch step {
			case "verify":
				row.Text = s.i18n.T(lang, "updf.run.busy_verify")
			case "start":
				row.Text = s.i18n.T(lang, "updf.run.busy_start")
			}
		default:
			row.State, row.Text = "wait", s.i18n.T(lang, "updf.run.wait")
			if step == "start" {
				row.Text = s.i18n.T(lang, "updf.run.wait_start")
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// displayNames lists the displays the installation interrupts.
func (s *server) displayNames(p principal) string {
	ds, err := s.db.Displays(p.household.ID)
	if err != nil {
		return ""
	}
	var names []string
	for _, d := range ds {
		names = append(names, d.Name)
	}
	return strings.Join(names, ", ")
}

// renderInstallPage shows the confirmation of a waiting file, or the progress
// of the installation of one.
func (s *server) renderInstallPage(w http.ResponseWriter, r *http.Request, p principal, status int) {
	s.settleUpload()
	u, ok, err := s.db.UpdateUpload()
	if err != nil {
		s.fail(w, "update upload", err)
		return
	}
	if !ok || !s.uploadsPossible() {
		http.Redirect(w, r, uploadPagePath, http.StatusSeeOther)
		return
	}
	lang := s.langOf(r)
	box := s.outboundBox()
	st := update.ReadStatus(s.updateDir)
	from := u.FromVersion
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Version": u.Version, "From": from,
		"Title":     s.i18n.Format(lang, i18n.Msg("updf.install.title", "version", u.Version)),
		"Installed": s.installedFact(r, p, st, box), "NotesURL": releasesURL + u.Version,
		"Guide": s.i18n.T(lang, guideUpdateAnchor), "ProgressURL": "/api/v1/update/file",
	}
	if rel, err := time.Parse("2006-01-02", u.Released); err == nil {
		data["Released"] = datefmt.Date(p.household.DateFormat, rel)
	}
	if u.State == store.UploadReady {
		data["State"] = "confirm"
		data["FileName"], data["MB"] = u.FileName, fileMegabytes(u.Size)
		data["Signer"] = s.i18n.Format(lang, i18n.Msg("updf.install.signer", "version", u.Version))
		data["Displays"] = s.displayNames(p)
	} else {
		data["State"] = "installing"
		data["Title"] = s.i18n.Format(lang, i18n.Msg("updf.running.title", "version", u.Version))
		started := datefmt.ClockSeconds(p.household.TimeFormat, u.StartedAt.In(p.location()))
		if u.StartedBy == p.person.Name {
			data["Lead"] = s.i18n.Format(lang, i18n.Msg("updf.running.lead", "time", started))
		} else {
			data["Lead"] = s.i18n.Format(lang, i18n.Msg("updf.running.lead_other", "name", u.StartedBy, "time", started))
		}
		data["Run"] = s.installingRows(lang, st, u, p)
		data["Step"] = installStep(st, u)
	}
	s.render(w, r, status, "update_install.html", data)
}

func (s *server) handleInstallPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderInstallPage(w, r, p, http.StatusOK)
}

// handleInstall is the parent's yes: the file waits, is the one that was
// checked, and the host script is asked to install it.
func (s *server) handleInstall(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if !uploadOverTLS(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	switch s.startInstall(p) {
	case startNone:
		http.Redirect(w, r, uploadPagePath, http.StatusSeeOther)
	case startFailed:
		s.fail(w, "start the installation", errors.New("could not start"))
	default:
		http.Redirect(w, r, installPath, http.StatusSeeOther)
	}
}

type startResult int

const (
	startNone    startResult = iota // no file is waiting (any more)
	startedNow                      // the host script was asked
	startRunning                    // an installation is running already
	startFailed
)

func (s *server) startInstall(p principal) startResult {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.settleLocked()
	if !s.uploadsPossible() || !s.hostStaysOffline() || s.rollbackFailed() {
		return startNone
	}
	u, ok, err := s.db.UpdateUpload()
	if err != nil {
		return startFailed
	}
	if !ok {
		return startNone
	}
	if u.State == store.UploadInstalling {
		return startRunning
	}
	// The file must be the one that was checked.
	if sum, err := fileSHA256(s.pendingPath()); err != nil || sum != u.SHA256 {
		s.logger.Warn("the update file changed after the check; deleting it", "err", err)
		s.dropUpload()
		return startNone
	}
	started, err := s.db.StartUpdateUpload(p.person.Name, s.now())
	if err != nil {
		return startFailed
	}
	if !started {
		return startRunning
	}
	s.syncUpdateSettings() // the request goes out with the settings
	return startedNow
}

func (s *server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	_, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if s.discardUpload() {
		s.redirectSaved(w, r, uploadPagePath, "flash.update_discarded")
		return
	}
	http.Redirect(w, r, uploadPagePath, http.StatusSeeOther)
}

// discardUpload deletes a waiting file; one that is being installed stays.
func (s *server) discardUpload() bool {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	u, ok, err := s.db.UpdateUpload()
	if err != nil || !ok || u.State != store.UploadReady {
		return false
	}
	s.dropUpload()
	return true
}
