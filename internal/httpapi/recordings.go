package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/zeggriim2/agentic-os/internal/recordings"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Enregistrements du QA navigateur (1.9.0) : lecture seule, fichiers copiés et validés par le moteur
// dans le stockage d'Agentic OS (jamais le dossier monté chez l'agent).

type recordingFileView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Label     string `json:"label,omitempty"`
	Criterion *int   `json:"criterion,omitempty"`
}

type recordingRunView struct {
	RunID     int64               `json:"run_id"`
	CreatedAt string              `json:"created_at"`
	ExpiresAt string              `json:"expires_at"`
	Report    *recordings.Report  `json:"report"`
	Video     *recordingFileView  `json:"video"`
	Trace     *recordingFileView  `json:"trace"`
	Scenario  *recordingFileView  `json:"scenario"`
	Shots     []recordingFileView `json:"shots"`
	Logs      []recordingFileView `json:"logs"`
}

// listTicketRecordings : GET /api/tickets/{id}/recordings — sessions QA enregistrées (récentes d'abord).
func (s *Server) listTicketRecordings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := store.GetTicket(r.Context(), s.Store.Pool, id); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := store.ListTicketRecordings(r.Context(), s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := []*recordingRunView{}
	byRun := map[int64]*recordingRunView{}
	for _, rec := range rows {
		v := byRun[rec.RunID]
		if v == nil {
			v = &recordingRunView{RunID: rec.RunID, CreatedAt: ts(rec.CreatedAt), ExpiresAt: ts(rec.ExpiresAt),
				Shots: []recordingFileView{}, Logs: []recordingFileView{}}
			byRun[rec.RunID] = v
			items = append(items, v)
		}
		f := &recordingFileView{ID: rec.ID, Name: rec.Name, Size: rec.SizeBytes, Label: rec.Label, Criterion: rec.Criterion}
		switch rec.Kind {
		case "video":
			v.Video = f
		case "trace":
			v.Trace = f
		case "scenario":
			v.Scenario = f
		case "screenshot":
			v.Shots = append(v.Shots, *f)
		case "log":
			v.Logs = append(v.Logs, *f)
		case "report":
			v.Report = s.readReport(rec)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Server) readReport(rec store.QARecording) *recordings.Report {
	if s.Recordings == "" {
		return nil
	}
	f, err := recordings.OpenStored(s.Recordings, rec.RunID, rec.Name)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rep recordings.Report
	if json.NewDecoder(io.LimitReader(f, recordings.MaxReport)).Decode(&rep) != nil {
		return nil
	}
	return &rep
}

var recordingTypes = map[string]string{
	"video": "video/webm", "trace": "application/zip", "screenshot": "image/png",
	"report": "application/json", "scenario": "text/plain; charset=utf-8", "log": "text/plain; charset=utf-8",
}

// recordingFile : GET /api/recordings/{id}/file — contenu d'un fichier (type fixé par le moteur, jamais
// deviné ; isolé par CSP sandbox ; traces, scénarios et journaux en pièce jointe).
func (s *Server) recordingFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, err := store.GetRecording(r.Context(), s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Recordings == "" {
		writeError(w, 404, "not_found", "enregistrements non configurés")
		return
	}
	f, err := recordings.OpenStored(s.Recordings, rec.RunID, rec.Name)
	if err != nil {
		writeError(w, 404, "not_found", "fichier d'enregistrement introuvable")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		writeError(w, 404, "not_found", "fichier d'enregistrement introuvable")
		return
	}
	h := w.Header()
	h.Set("Content-Type", recordingTypes[rec.Kind])
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, max-age=3600")
	base := rec.Name[strings.LastIndex(rec.Name, "/")+1:]
	disp := "inline"
	if rec.Kind == "trace" || rec.Kind == "scenario" || rec.Kind == "log" {
		disp = "attachment"
	}
	h.Set("Content-Disposition", disp+`; filename="run-`+strconv.FormatInt(rec.RunID, 10)+"-"+base+`"`)
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// traceViewerHeaders : le visualiseur de traces Playwright (statique, /trace-viewer/) a besoin d'un
// service worker, de cadres de même origine et de blobs ; ses données viennent de /api/recordings.
func traceViewerHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; "+
		"media-src 'self' blob:; font-src 'self' data:; connect-src 'self' blob: data:; worker-src 'self'; frame-src 'self' blob: data:; frame-ancestors 'self'")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Cache-Control", "no-cache")
}
