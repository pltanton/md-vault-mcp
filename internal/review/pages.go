package review

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/pltanton/md-vault-mcp/internal/index"
)

//go:embed templates/*.html
var templates embed.FS

var statusNames = map[string]string{
	"pending": "pending", "applied": "applied", "rejected": "rejected", "stale": "stale", "expired": "expired",
}

func parsePages() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"status": func(s string) string {
			if n, ok := statusNames[s]; ok {
				return n
			}
			return s
		},
		"add": func(a, b int) int { return a + b },
		"when": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
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
	Edits  []editView
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

func (s *Server) list(w http.ResponseWriter, r *http.Request, v viewer) {
	pending, decided, err := s.queue.ListProposals(r.Context())
	if err != nil {
		s.message(w, http.StatusInternalServerError, v, "Something went wrong", err.Error())
		return
	}
	s.render(w, http.StatusOK, "list.html", listPage{Title: "Agent proposals", Viewer: v, Pending: pending, Decided: decided})
}

func (s *Server) show(w http.ResponseWriter, r *http.Request, v viewer) {
	s.showProposal(w, r, v, http.StatusOK, "")
}

func (s *Server) showProposal(w http.ResponseWriter, r *http.Request, v viewer, status int, errText string) {
	p, ok, err := s.queue.GetProposal(r.Context(), r.PathValue("id"))
	if err != nil {
		s.message(w, http.StatusInternalServerError, v, "Something went wrong", err.Error())
		return
	}
	if !ok {
		s.message(w, http.StatusNotFound, v, "No such proposal", "The link may be incomplete.")
		return
	}
	var edits []editView
	if p.Status == "pending" {
		for _, e := range s.queue.PreviewProposal(p) {
			edits = append(edits, buildEdit(e))
		}
	}
	s.render(w, status, "proposal.html", proposalPage{Title: p.Path, Viewer: v, P: p, Edits: edits, Error: errText})
}

func (s *Server) decide(apply bool) func(http.ResponseWriter, *http.Request, viewer) {
	return func(w http.ResponseWriter, r *http.Request, v viewer) {
		id := r.PathValue("id")
		comment := strings.TrimSpace(r.PostFormValue("comment"))
		var err error
		if apply {
			_, err = s.queue.ApplyProposal(r.Context(), id, v.User, comment)
		} else {
			_, err = s.queue.RejectProposal(r.Context(), id, v.User, comment)
		}
		if err != nil && !errors.Is(err, index.ErrNotPending) {
			s.showProposal(w, r, v, http.StatusInternalServerError, err.Error())
			return
		}
		http.Redirect(w, r, "/p/"+id, http.StatusSeeOther)
	}
}
