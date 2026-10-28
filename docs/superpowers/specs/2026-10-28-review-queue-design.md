# Review queue for the agent's edits of foreign text

Status: approved 2026-10-28. Sub-project 2 of 2; builds on the Postgres index
from `2026-10-28-fulltext-search-design.md`. Decisions were taken on the design
page https://claude.ai/artifact/SwkPmPFupruMAQmpQynQGp.

## Goal

Let the agent change any text, delete and move notes wherever the write rules
allow, without touching the owner's text behind the owner's back: everything
beyond what it may do today becomes a proposal that a person approves or
rejects in a web UI, signed in through Kanidm.

## Decisions

- What the agent does directly stays as it is: its own blocks within the edit
  window (`note_edit`, `note_remove`), appending (`note_add`, `note_create`) and
  front matter properties. Everything else is a proposal.
- Write rules still bound everything: a proposal is accepted only for notes the
  instance may write, and a move only to a writable path.
- An edit is a list of `{old, new}` replacements; each `old` occurs exactly once
  in the note and edits do not overlap.
- On approval a proposal is applied only if what it was made against is still
  there; otherwise it becomes `stale` and the agent proposes again. No merging.
- The UI applies or rejects, with an optional comment; it does not edit the
  proposed text.
- The agent learns of nothing by push: proposing returns a URL, the agent puts
  it in its reply; `note_proposals` shows outcomes.
- After a decision the proposal's text (`edits`, `link_edits`, `reason`) is
  erased; status, who decided, when and the comment stay.
- Anyone the instance's OIDC client admits may approve. Kanidm admits `admins`
  plus the client's groups: `personal` admits `admins` (Anton), `family` admits
  `admins` and `family` (Anton, Yulia). The server checks the `groups` claim
  against its own list as well.
- Login is the server's own OIDC client (code flow with PKCE), not a proxy
  check.
- Deleting and moving notes go through review too. A move rewrites the links
  to the note in the same proposal, found through a links index.
- Amended for plan B: there is no `links` table. Backlinks are found by walking
  the readable notes when a move or delete is proposed (milliseconds for a
  vault of this size, and never stale), and `file.backlinks` is computed from
  the notes `base_query` already loads. The `links` DDL below is not created.
- Work is split into two plans: A — edit proposals, UI, login, NixOS; B — links
  index, `file.backlinks`, delete and move proposals.

## Data

Both tables live in the instance's database next to the search index and are
filled by the same indexer (startup `Sync`, watcher, reindex after the agent's
writes).

```sql
CREATE TABLE links (
  path     text NOT NULL REFERENCES files (path) ON DELETE CASCADE,
  line     int  NOT NULL,
  target   text NOT NULL,
  resolved text,
  PRIMARY KEY (path, line, target)
);
CREATE INDEX links_resolved ON links (resolved);

CREATE TABLE proposals (
  id          text PRIMARY KEY,
  kind        text NOT NULL CHECK (kind IN ('edit', 'delete', 'move')),
  path        text NOT NULL,
  new_path    text,
  status      text NOT NULL CHECK (status IN ('pending', 'applied', 'rejected', 'stale', 'expired')),
  edits       jsonb,
  link_edits  jsonb,
  reason      text,
  base_hash   text,
  result      jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  decided_at  timestamptz,
  decided_by  text,
  comment     text
);
CREATE INDEX proposals_status ON proposals (status, created_at);
```

- `links.target` is the link as written without alias and heading (`Мама`,
  `Люди/Мама`, `Люди/Мама.md`); `resolved` is the note path it points to or
  null. Resolution follows Obsidian: a target with a `/` is a path from the
  vault root (`.md` appended when missing); a bare name resolves to the one note
  with that file name, and stays null when several have it. Markdown links
  `[x](y.md)` resolve relative to the note's folder first, then to the vault
  root, with `%20` decoded. Wikilinks `[[…]]`, embeds `![[…]]` and Markdown
  links to notes are indexed; links inside fenced or inline code are not.
- `links` holds link targets, which are note names and paths, not note text.
  Only readable notes are indexed, so links from notes the instance cannot read
  are unknown.
- `proposals.id` is 16 random hex characters; the URL is
  `<review.public_url>/p/<id>`.
- `base_hash` is an HMAC-SHA256 of the whole note with the journal's key, taken
  when a delete or move is proposed.
- `result` records what applying did: for a move, the notes whose links could
  not be rewritten.
- A pending proposal older than 30 days becomes `expired` (checked at startup
  and hourly), with its text erased like after a decision.

## Agent tools

Registered only when `[review]` is configured.

```
note_propose(path, edits: [{old, new}], reason)  → {id, url, status}
note_propose_delete(path, reason)                 → {id, url, status}               (plan B)
note_propose_move(path, new_path, reason)         → {id, url, status, links, unwritable}  (plan B)
note_proposals(status?, limit?)                   → {proposals: [{id, kind, path, new_path, status, comment, decided_by, decided_at, url}]}
```

- `note_propose` validates at once: the note is writable, every `old` is
  non-empty and occurs exactly once, edits do not overlap, `reason` is not empty.
  A violation is an error to the agent, not a proposal.
- If every edit lies inside one of the agent's live blocks (located the way
  `note_edit` locates them), it is applied directly, the blocks' journal
  entries are updated, and the result has `status: applied` and no URL.
- `note_propose_move` collects, from `links` where `resolved = path`, the link
  rewrites in every writable note and stores them in `link_edits`; `links` is
  their count, `unwritable` lists notes with links it may not change.
- A rewritten link keeps its form: `#heading`, `|alias` and the `!` of an embed
  stay; a bare-name wikilink stays a bare name if the new file name is unique
  among notes, otherwise it becomes the vault path without `.md`; a path
  wikilink stays a path; a Markdown link keeps being relative or rooted as it
  was.
- `note_proposals` lists newest first, default 20, at most 50; by default all
  statuses.

## Applying

Run by the UI on "apply", inside the journal's lock like every write.

- **edit**: read the note; if each `old` still occurs exactly once, replace and
  write atomically (the existing `writeFile`), else `stale`.
- **delete** (B): if the note's HMAC equals `base_hash`, remove the file, else
  `stale`.
- **move** (B): the source's HMAC equals `base_hash`, the target does not exist
  and is still writable — else `stale`. Rename, then apply each note's link
  edits; a note whose link fragment no longer occurs exactly once is skipped
  and listed in `result`. The move stays applied.
- After every write: reindex the touched notes (search and links). The agent's
  journal entries follow a moved note's path and are dropped for a deleted
  note.
- Then set `status`, `decided_at`, `decided_by` (`preferred_username`),
  `comment`, and null `edits`, `link_edits` and `reason`.
- "reject" sets `rejected` with the same bookkeeping and touches no file.

## Web UI and login

Config:

```toml
[review]
listen = "127.0.0.1:8193"
public_url = "https://family-notes.pltanton.dev"
allowed_groups = ["admins", "family"]
oidc_issuer = "https://home.id.pltanton.dev/oauth2/openid/family-notes"
oidc_client_id = "family-notes"
oidc_client_secret_file = "/run/credentials/md-vault-mcp-family.service/oidc"
```

- A second HTTP server on `review.listen`; it never serves `/mcp` and never
  accepts the agent's bearer token. The MCP server has no tool that applies or
  rejects.
- Login: OIDC authorization code flow with PKCE S256 (Kanidm requires it), via
  `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2`; scopes `openid
profile groups`. `state`, `nonce` and the PKCE verifier live in a 10-minute
  HMAC-signed cookie. The ID token's `groups` (values `name` or
  `name@domain`; the part before `@` is compared) must intersect
  `allowed_groups`.
- Session: an HMAC-signed cookie holding `preferred_username`, groups and an
  expiry 12 hours out; `Secure`, `HttpOnly`, `SameSite=Lax`, path `/`. The key
  is 32 random bytes in `<state_dir>/review.key`, created on first start like
  the journal key. Every POST carries a CSRF token derived from the session.
- Pages, server-rendered with `html/template`, no JavaScript:
  - `/` — pending proposals, then the 20 most recently decided.
  - `/p/<id>` — path, kind, reason, and for an edit each replacement as a diff
    with two lines of context taken from the note as it is now; a replacement
    whose `old` no longer occurs exactly once is shown as stale. A move shows
    the rename, every link edit and the `unwritable` notes. Apply and reject
    buttons with a comment field while pending.
  - `POST /p/<id>/apply`, `POST /p/<id>/reject` — redirect back to `/p/<id>`.
  - `/login`, `/callback`, `POST /logout`.
- Unauthenticated requests redirect to `/login?next=<path>`; `next` must be a
  local path.

## NixOS

- Instance options `review = { enable; listen; publicUrl; allowedGroups;
oidc = { issuer; clientId; clientSecretFile; }; }`; the secret goes through
  `LoadCredential` like the token.
- In the server config (not this repo): clients `notes` (groups `[]`) and
  `family-notes` (groups `["family"]`) in `sso.nix` with the instance's group
  as `secretGroup`, and Caddy virtual hosts proxying to `review.listen`.

## Errors

- Database or file errors while applying: the proposal stays `pending`, the UI
  shows the error; nothing is half-applied for an edit (single atomic write).
  For a move, a failure after the rename is recorded in `result` and the
  proposal is `applied`.
- OIDC provider unreachable at startup: the review server logs and retries
  discovery on the next login instead of failing the MCP server.
- A login whose groups do not match: 403 page naming the account.

## Testing

- Proposal validation: table tests for missing, duplicated and overlapping
  `old` fragments, unwritable paths, the own-block shortcut.
- Applying: pgtest — applied, stale after the owner changed the fragment,
  applied after an unrelated change elsewhere in the note, text erased after
  each outcome, expiry.
- Links (B): extraction and resolution table tests (wikilinks with heading and
  alias, embeds, Markdown links, code spans, ambiguous names); rewrite table
  tests for each link form; move with a skipped note.
- Web: handlers with an `httptest` fake OIDC provider (discovery, JWKS, token
  endpoint signing ID tokens with a test key): login, group mismatch, CSRF
  rejection, apply and reject, `next` redirect safety.

## Out of scope

Editing proposals in the UI; push notifications; merging stale proposals;
per-folder approver roles; fixing the server's `local all all trust` in
`pg_hba`.
