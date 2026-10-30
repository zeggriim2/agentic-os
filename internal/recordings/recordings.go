// Package recordings gère les enregistrements du QA navigateur : dossier de dépôt monté chez l'agent
// (inbox), validation stricte des fichiers déposés, copie dans le stockage d'Agentic OS (store, jamais
// monté chez un agent) et purge.
//
// Arborescence sous Root :
//
//	inbox/<projet>/<clé du ticket>-j<job>-a<tentative>/   écrit par l'agent QA (montage rw)
//	store/<run id>/                                       copie validée, servie par l'API
package recordings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Retention : durée de conservation des enregistrements.
const Retention = 15 * 24 * time.Hour

// Limites (un enregistrement normal pèse quelques Mo).
const (
	MaxVideo    = 100 << 20
	MaxTrace    = 100 << 20
	MaxShot     = 10 << 20
	MaxShots    = 40
	MaxReport   = 1 << 20
	MaxScenario = 200 << 10
	MaxLog      = 1 << 20
	MaxTotal    = 300 << 20
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	keyRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,80}$`)
	shotRe = regexp.MustCompile(`^shots/[a-z0-9][a-z0-9._-]{0,76}\.png$`)
	logRe  = regexp.MustCompile(`^logs/[a-z0-9][a-z0-9_-]{0,40}\.log$`)
	// NameRe : noms acceptés (même règle que la contrainte de la table qa_recordings).
	NameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,80}(/[a-z0-9][a-z0-9._-]{0,80})?$`)
)

// File est un fichier d'enregistrement validé et copié dans le stockage.
type File struct {
	Kind      string // video | trace | screenshot | report | scenario | log
	Name      string
	Label     string
	Criterion *int
	Size      int64
	SHA256    string
}

// Report est le rapport écrit par l'exécuteur de scénario (report.json).
type Report struct {
	OK              bool     `json:"ok"`
	Error           *string  `json:"error"`
	DurationMS      int64    `json:"duration_ms"`
	Steps           []Step   `json:"steps"`
	Shots           []Shot   `json:"shots"`
	ConsoleErrors   []string `json:"console_errors"`
	FailedRequests  []string `json:"failed_requests"`
	BlockedExternal int      `json:"blocked_external"`
}

// Step est une étape nommée du scénario.
type Step struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	MS    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}

// Shot décrit une capture (fichier, libellé, critère illustré).
type Shot struct {
	File      string `json:"file"`
	Label     string `json:"label"`
	Criterion *int   `json:"criterion"`
}

// InboxDir renvoie le dossier de dépôt d'un passage QA.
func InboxDir(root, slug, ticketKey string, jobID int64, attempt int) (string, error) {
	if !slugRe.MatchString(slug) || !keyRe.MatchString(ticketKey) {
		return "", fmt.Errorf("projet ou ticket invalide pour un dossier d'enregistrement")
	}
	return filepath.Join(root, "inbox", slug, fmt.Sprintf("%s-j%d-a%d", ticketKey, jobID, attempt)), nil
}

// ProjectInbox renvoie le dossier de dépôt d'un projet (celui monté chez l'agent QA).
func ProjectInbox(root, slug string) string { return filepath.Join(root, "inbox", slug) }

// StoreDir renvoie le dossier de stockage d'un run.
func StoreDir(root string, runID int64) string {
	return filepath.Join(root, "store", fmt.Sprintf("%d", runID))
}

// Prepare crée un dossier de dépôt vide (un reste d'une tentative précédente est supprimé).
func Prepare(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// classify renvoie le type d'un fichier déposé et sa taille maximale ("" : refusé).
func classify(name string) (string, int64) {
	switch {
	case name == "video.webm":
		return "video", MaxVideo
	case name == "trace.zip":
		return "trace", MaxTrace
	case name == "report.json":
		return "report", MaxReport
	case name == "scenario.mjs":
		return "scenario", MaxScenario
	case shotRe.MatchString(name):
		return "screenshot", MaxShot
	case logRe.MatchString(name):
		return "log", MaxLog
	}
	return "", 0
}

var magic = map[string][]byte{
	"video":      {0x1A, 0x45, 0xDF, 0xA3},
	"trace":      {'P', 'K', 0x03, 0x04},
	"screenshot": {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
}

// Ingest valide le contenu d'un dossier de dépôt et copie les fichiers acceptés dans dst. Les
// fichiers hors liste, liens, fichiers spéciaux ou trop gros sont ignorés et signalés dans skipped.
// Libellés et critères des captures viennent de report.json (validés).
func Ingest(src, dst string) (files []File, report *Report, skipped []string, err error) {
	if err := os.MkdirAll(filepath.Join(dst, "shots"), 0o750); err != nil {
		return nil, nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(dst, "logs"), 0o750); err != nil {
		return nil, nil, nil, err
	}
	var total int64
	shots := 0
	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if rel == "shots" || rel == "logs" {
				return nil
			}
			skipped = append(skipped, rel+"/")
			return filepath.SkipDir
		}
		kind, max := classify(rel)
		info, ierr := os.Lstat(p)
		if kind == "" || ierr != nil || !info.Mode().IsRegular() {
			skipped = append(skipped, rel)
			return nil
		}
		if info.Size() > max || total+info.Size() > MaxTotal || (kind == "screenshot" && shots >= MaxShots) {
			skipped = append(skipped, rel+" (trop volumineux)")
			return nil
		}
		f, cerr := copyChecked(p, filepath.Join(dst, filepath.FromSlash(rel)), max, magic[kind])
		if cerr != nil {
			skipped = append(skipped, rel+" ("+cerr.Error()+")")
			return nil
		}
		f.Kind, f.Name = kind, rel
		total += f.Size
		if kind == "screenshot" {
			shots++
		}
		files = append(files, f)
		return nil
	})
	if walkErr != nil {
		return nil, nil, skipped, walkErr
	}
	for i := range files {
		if files[i].Kind != "report" {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dst, "report.json"))
		var r Report
		if rerr == nil && json.Unmarshal(b, &r) == nil {
			report = &r
		} else {
			skipped = append(skipped, "report.json (illisible)")
		}
	}
	if report != nil {
		byFile := map[string]Shot{}
		for _, s := range report.Shots {
			byFile[s.File] = s
		}
		for i := range files {
			if s, ok := byFile[files[i].Name]; ok && files[i].Kind == "screenshot" {
				files[i].Label = truncate(strings.TrimSpace(s.Label), 200)
				if s.Criterion != nil && *s.Criterion > 0 && *s.Criterion < 1000 {
					c := *s.Criterion
					files[i].Criterion = &c
				}
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, report, skipped, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// copyChecked copie src vers dst (taille bornée, en-tête vérifié) et calcule le SHA-256.
func copyChecked(src, dst string, max int64, head []byte) (File, error) {
	in, err := os.Open(src)
	if err != nil {
		return File{}, err
	}
	defer in.Close()
	if len(head) > 0 {
		buf := make([]byte, len(head))
		if _, err := io.ReadFull(in, buf); err != nil || !bytes.Equal(buf, head) {
			return File{}, errors.New("format inattendu")
		}
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return File{}, err
		}
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return File{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, max+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("trop volumineux")
	}
	if err != nil {
		_ = os.Remove(dst)
		return File{}, err
	}
	return File{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// OpenStored ouvre un fichier du stockage d'un run (nom revalidé : aucun chemin arbitraire).
func OpenStored(root string, runID int64, name string) (*os.File, error) {
	if !NameRe.MatchString(name) || strings.Contains(name, "..") {
		return nil, errors.New("nom d'enregistrement invalide")
	}
	return os.Open(filepath.Join(StoreDir(root, runID), filepath.FromSlash(name)))
}

// PurgeInbox supprime les dossiers de dépôt plus vieux que maxAge (passages QA interrompus).
func PurgeInbox(root string, maxAge time.Duration, now time.Time) (int, error) {
	n := 0
	projects, err := os.ReadDir(filepath.Join(root, "inbox"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dirs, err := os.ReadDir(filepath.Join(root, "inbox", p.Name()))
		if err != nil {
			continue
		}
		for _, d := range dirs {
			info, err := d.Info()
			if err == nil && d.IsDir() && now.Sub(info.ModTime()) > maxAge {
				if os.RemoveAll(filepath.Join(root, "inbox", p.Name(), d.Name())) == nil {
					n++
				}
			}
		}
	}
	return n, nil
}
