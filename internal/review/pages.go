package review

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

//go:embed templates/*.html
var templates embed.FS

var statusNames = map[string]string{
	"pending": "ждёт", "applied": "применено", "rejected": "отклонено", "stale": "устарело", "expired": "истекло",
}

func parsePages() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"status": func(s string) string {
			if n, ok := statusNames[s]; ok {
				return n
			}
			return s
		},
		"when": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("02.01.2006 15:04")
		},
	}).ParseFS(templates, "templates/*.html"))
}

type viewer struct {
	User string
	CSRF string
}

type messagePage struct {
	Title  string
	Viewer viewer
	Text   string
}

type listPage struct {
	Title            string
	Viewer           viewer
	Pending, Decided []index.Proposal
}

type proposalPage struct {
	Title  string
	Viewer viewer
	P      index.Proposal
	Edits  []notes.EditPreview
	Error  string
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var b bytes.Buffer
	if err := s.pages.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b.Bytes())
}

func (s *Server) message(w http.ResponseWriter, status int, v viewer, title, text string) {
	s.render(w, status, "message.html", messagePage{Title: title, Viewer: v, Text: text})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, v viewer) {}
func (s *Server) show(w http.ResponseWriter, r *http.Request, v viewer) {}
func (s *Server) decide(apply bool) func(http.ResponseWriter, *http.Request, viewer) {
	return func(http.ResponseWriter, *http.Request, viewer) {}
}
