package notes

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type readInput struct {
	Path   string `json:"path" jsonschema:"note path relative to the vault, e.g. 00. Inbox/Idea.md"`
	Offset int    `json:"offset,omitempty" jsonschema:"1-based line to start from"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many lines to return"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"case-insensitive substring to look for in note text and file names"`
	Under string `json:"under,omitempty" jsonschema:"directory to search in; omit for every readable note"`
	Limit int    `json:"limit,omitempty" jsonschema:"at most this many matches, up to 50"`
}

type listInput struct {
	Dir string `json:"dir,omitempty" jsonschema:"directory relative to the vault; omit for the root"`
}

type addInput struct {
	Path    string `json:"path" jsonschema:"existing note to add to"`
	Text    string `json:"text" jsonschema:"one or more lines to add"`
	Heading string `json:"heading,omitempty" jsonschema:"heading line exactly as in the note, e.g. '# Gift ideas'; the text goes to the end of that section. Omit to add at the end of the note"`
}

type createInput struct {
	Path string `json:"path" jsonschema:"new note path; must not exist yet"`
	Text string `json:"text" jsonschema:"the whole note"`
}

type editInput struct {
	ID   string `json:"id" jsonschema:"block id from note_add, note_create or note_recent"`
	Text string `json:"text" jsonschema:"new text replacing the whole block"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"block id from note_add, note_create or note_recent"`
}

type setPropertyInput struct {
	Path  string `json:"path" jsonschema:"note to change"`
	Name  string `json:"name" jsonschema:"property name as in the front matter, e.g. birthday"`
	Value any    `json:"value" jsonschema:"text, number, true/false, null or a list of those; dates as YYYY-MM-DD, links as [[Note]]"`
}

type unsetPropertyInput struct {
	Path string `json:"path" jsonschema:"note to change"`
	Name string `json:"name" jsonschema:"property to remove"`
}

type baseInput struct {
	Path  string `json:"path" jsonschema:"the .base file, e.g. 02. Areas/Отношения/00. Дни рождений.base"`
	View  string `json:"view,omitempty" jsonschema:"view name; omit for the first view"`
	Limit int    `json:"limit,omitempty" jsonschema:"at most this many rows, up to 500"`
}

type recentInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"at most this many blocks, up to 20"`
}

func (s *Service) Register(server *mcp.Server) {
	window := s.window.String()

	mcp.AddTool(server, &mcp.Tool{
		Name:        "note_read",
		Description: "Read a note. Lines come numbered (number, tab, text); the numbers are not part of the note.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, ReadResult, error) {
		out, err := s.Read(in.Path, in.Offset, in.Limit)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_search",
		Description: "Find notes by a case-insensitive substring of their text or file name. Returns path, " +
			"line and the matching line. Only notes you may read are searched.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, SearchResult, error) {
		out, err := s.Search(in.Query, in.Under, in.Limit)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "note_list",
		Description: "List the notes and subdirectories of a directory. Only what you may read is shown.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, ListResult, error) {
		out, err := s.List(in.Dir)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_add",
		Description: "Add lines to an existing note: at the end of the section under `heading`, or at the end " +
			"of the note. Returns a block id; with it you may change or remove exactly these lines for " +
			window + " after writing them, as long as nobody else touched them.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, WriteResult, error) {
		out, err := s.Add(in.Path, in.Text, in.Heading)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_create",
		Description: "Create a new note; fails if it exists. The whole note becomes one block you may change " +
			"or remove for " + window + ".",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, WriteResult, error) {
		out, err := s.Create(in.Path, in.Text)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_edit",
		Description: "Replace a block you wrote with new text. Works only within " + window + " of writing it " +
			"and only while the block is exactly as you left it; once the owner changed it, it is his.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in editInput) (*mcp.CallToolResult, WriteResult, error) {
		out, err := s.Edit(in.ID, in.Text)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "note_remove",
		Description: "Remove a block you wrote, under the same conditions as note_edit. For a property change id it puts the previous value back.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, WriteResult, error) {
		out, err := s.Remove(in.ID)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_properties",
		Description: "Query note properties (YAML front matter) without reading the notes: filter by folder, tag, " +
			"set or equal properties, or by a yearly date coming up in the next days (birthdays), and get back " +
			"only the fields you ask for. Dates, ages and sorting are computed here — do not recompute them.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in PropertyQuery) (*mcp.CallToolResult, PropertyResult, error) {
		out, err := s.Properties(in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_set_property",
		Description: "Set one property in a note's front matter, adding the front matter if the note has none. " +
			"Other properties and the note text stay untouched. Returns the previous value and an id; " +
			"note_remove(id) puts the previous value back within " + window + " unless someone changed it since.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in setPropertyInput) (*mcp.CallToolResult, PropertyChange, error) {
		out, err := s.SetProperty(in.Path, in.Name, in.Value)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "note_unset_property",
		Description: "Remove one property from a note's front matter. Undo it with note_remove(id) like note_set_property.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in unsetPropertyInput) (*mcp.CallToolResult, PropertyChange, error) {
		out, err := s.UnsetProperty(in.Path, in.Name)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "base_query",
		Description: "Run an Obsidian Bases view (.base file) and get its table: the base's filters, formulas, " +
			"sorting, grouping and summaries are evaluated here, columns are the view's order. Only notes you may " +
			"read take part. Fails naming the construct if the base uses something this server does not support.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in baseInput) (*mcp.CallToolResult, BaseResult, error) {
		out, err := s.QueryBase(in.Path, in.View, in.Limit)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "note_recent",
		Description: "Blocks you wrote and property changes you made that you can still change or undo, newest " +
			"first, with their current text. Use it to find \"what you just added\".",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in recentInput) (*mcp.CallToolResult, RecentResult, error) {
		out, err := s.Recent(in.Limit)
		return nil, out, err
	})
}

func NewHTTPHandler(server *mcp.Server, token string) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}
