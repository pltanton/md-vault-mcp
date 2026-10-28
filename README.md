# md-vault-mcp

> [!WARNING]
> **AI slop.** This project was written almost entirely by an AI agent and has
> not been properly reviewed by a human. It works for the author's setup, but
> expect bugs, rough edges and security mistakes. Read the code before you
> point it at notes you care about, and don't rely on it as a security boundary
> without your own audit.

An [MCP](https://modelcontextprotocol.io) server that lets an AI agent work
with a folder of Markdown notes (an Obsidian vault, for example) without
handing it the whole folder.

- **Scoped.** Separate read and write rules, as globs relative to the vault.
  Everything else is invisible: searches and listings never return names the
  agent may not read.
- **Append-mostly.** The agent can add lines to existing notes and create new
  ones. It can change or remove only the blocks it wrote itself, only for a
  limited time (48 hours by default) and only while nobody else has touched
  them. The owner's own text is never rewritten.
- **No copy of your text.** The journal of what the agent wrote stores keyed
  hashes and line counts; the search index stores word stems, paths, line
  ranges, sizes and modification times. Neither stores note text.
- **stdio or streamable HTTP**, the latter behind a bearer token.

It was written so that a personal agent whose tools run in a sandbox without
the vault mounted can still look things up and jot things down — in the
owner's personal notes only, never in the work folders that live in the same
vault.

## Tools

| Tool                                 | What it does                                                                                                                                                                                        |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `note_read(path, offset?, limit?)`   | Note content with line numbers.                                                                                                                                                                     |
| `note_search(query, under?, limit?)` | Full-text search: Russian and English word forms match, words may sit on different lines of one section. Sections ranked by relevance with heading, line range and up to 3 matching lines; notes whose file name matches. `"phrase"`, `-word`, `a or b`. |
| `note_list(dir?)`                    | Readable notes and the subdirectories that contain some.                                                                                                                                            |
| `note_add(path, text, heading?)`     | Append lines to an existing note: at the end of the section under `heading` (the heading line as written, e.g. `## Gift ideas`, or just its text), or at the end of the note. Returns a block `id`. |
| `note_create(path, text)`            | Create a note; fails if it exists. The whole note is one block.                                                                                                                                     |
| `note_edit(id, text)`                | Replace a block the agent wrote.                                                                                                                                                                    |
| `note_remove(id)`                    | Remove a block the agent wrote. A created note whose only block is removed is deleted.                                                                                                              |
| `note_recent(limit?)`                | Blocks the agent can still change, newest first, with their current text — for "rewrite what you just added".                                                                                       |
| `note_properties(under?, tag?, has?, equals?, fields?, upcoming?, sort?, limit?)` | Front matter of many notes at once, without their text. `upcoming: {field, days}` keeps notes whose yearly date (birthday) comes within `days` and adds the next date, days left and years. |
| `note_set_property(path, name, value)` | Set one front matter property; adds front matter if missing. Returns the previous value and an `id`; `note_remove(id)` undoes it. |
| `note_unset_property(path, name)`    | Remove one front matter property; undo the same way. |
| `base_query(path, view?, limit?)`   | Run a view of an Obsidian Bases file (`.base`) and return its table. |
| `note_propose(path, edits, reason)` | Propose replacing exact pieces of text the agent may not change directly; a person applies or rejects it on the review page. Applied at once when every edit is inside the agent's own live blocks. Returns the page's URL. |
| `note_propose_delete(path, reason)` | Propose deleting a note; the result lists the notes that link to it. |
| `note_propose_move(path, new_path, reason)` | Propose moving or renaming a note; the links to it in notes the agent may write are rewritten in the same proposal. |
| `note_proposals(status?, limit?)` | The agent's proposals with status (`pending`, `applied`, `rejected`, `stale`, `expired`) and the reviewer's comment. |

A section ends before the next heading of the same or a higher level, so it
includes its subsections. New lines go right after the last non-blank line of
the section, which keeps the blank lines before the next heading where they
were. Headings inside front matter and fenced code are ignored. Line endings
(LF or CRLF) and the presence of a final newline are preserved.

`base_query` evaluates the base itself: global and view filters (`and`, `or`,
`not`), formulas, `sort`, `groupBy`, `limit` and summaries (the built-in ones
and those defined in the base). Columns are the view's `order`, values come
typed as JSON (dates as `YYYY-MM-DD[ HH:mm:ss]`, links as `[[target]]`). Only
notes the client may read take part, and only Markdown notes — attachments a
base would list in Obsidian are not there. Supported: the operators, date
arithmetic with durations, `file.*` fields and `hasTag`, `hasLink`,
`hasProperty`, `inFolder`, `asLink`; `date`, `duration`, `today`, `now`, `if`,
`list`, `link`, `file`, `min`, `max`, `number`; the string, number, list
(including `map`, `filter`, `reduce`), date (`format` with Moment tokens,
`relative`) and object methods, and regular expressions; `file.backlinks`
counts links from notes the client may read. Not supported: `random()`. A base using an unsupported construct fails with
its name instead of returning a partial table; a value that cannot be computed
for one note is null and listed in `warnings`.

Property edits touch only the lines of that key: other keys, their order,
comments and quoting stay as written. Lists are written as block sequences and
dates unquoted, the way Obsidian writes them. Unlike blocks, any property in a
writable note may be changed; the undo works within the edit window while the
key still reads as the agent left it.

### Search

The index lives in Postgres (`database_url`). A note is cut into sections at
every heading (and at the front matter); a section longer than 40 lines is cut
into 40-line pieces. Words are stemmed with Snowball (Russian and English);
Russian forms whose stem Snowball splits from their lemma's (`подарки` and
`подарок`, `дня` and `день`, `рту` and `рот`) are joined first by the
[ru-stem-fixes](https://github.com/pltanton/ru-stem-fixes) synonym dictionary.
`ё` counts as `е`. At startup the server reindexes notes whose size or modification time
changed and drops those that are gone or no longer readable, then follows
changes with inotify; its own writes are reindexed before the tool returns.
Headings and matching lines are read from the files when a search runs.

`ru_stem_fixes.syn` must be in Postgres' `tsearch_data` directory; the NixOS
module from this flake installs it, and the dev shell's Postgres has it.

### How the agent's blocks are tracked

For each block the journal records: id, path, the heading it went under, line
count, an HMAC-SHA256 of the block text, and creation and update times. To edit
or remove a block, the server slides a window of that many lines over the
current file and compares hashes, preferring matches under the recorded
heading:

- exactly one match: the change goes through, and the journal gets the new hash;
- none: the owner has changed or removed it, and the block is now his;
- more than one: the server refuses and asks to clarify.

The edit window counts from the block's **creation**; edits do not extend it.
Expired entries are dropped from the journal. The HMAC key is generated on
first start and kept next to the journal, so the hashes of short lines (`- [ ]
milk`) cannot be guessed from the journal alone.

### Review

With `[review]` configured, the server also serves a page on its own listener
where people apply or reject the agent's proposals. Login is OpenID Connect
(code flow with PKCE) against the configured issuer; only members of
`allowed_groups` get in (a `groups` claim of `name@domain` counts as `name`),
and the agent's bearer token is never accepted there. Applying checks that
every replaced piece of text still occurs exactly once; otherwise the proposal
becomes `stale`. A decision erases the proposed text and the agent's reason,
keeping who decided, when and the comment; proposals left pending for 30 days
expire.

A delete or a move applies only if the note is byte for byte what it was when
proposed. A move rewrites wikilinks, embeds and Markdown links to the note,
keeping headings, aliases and the link's form (a bare `[[Name]]` stays bare
while the new name is unique); a note whose link changed in between is skipped
and listed. Links from notes the agent may not read are not seen, and relative
Markdown links inside the moved note itself are not adjusted.

## Access rules

```toml
[read]
mode = "allowlist"                 # or "denylist"
allow = ["00. Inbox/**", "02. Areas/Personal/**", "*.md"]
deny = ["**/Work/**"]

[write]
mode = "allowlist"
allow = ["00. Inbox/**"]
```

- Patterns are slash-separated and relative to the vault. `*` and `?` match
  within one path segment, `**` matches any number of segments, `[...]` is a
  character class. A trailing `/` means "everything below".
- A pattern that matches a directory covers everything below it, so `Work` and
  `Work/**` deny the same files.
- `allowlist`: a path is allowed if an `allow` pattern matches and no `deny`
  pattern does. `denylist`: allowed unless a `deny` pattern matches (`allow`
  is rejected in this mode).
- **Deny always wins.**
- Write access is always intersected with read access.
- Any path segment starting with a dot (`.obsidian`, `.trash`, `.stversions`,
  `.git`, …) is never accessible, whatever the rules say.
- Only files with the configured extensions (default `.md`) are notes.
- Paths with `..`, absolute paths and backslashes are rejected. Symlinks are
  resolved, and the target must be inside the vault and allowed as well. Walks
  (search, listing) do not follow symlinks.
- A denied note and a missing note produce the same error.

## Configuration

See [`md-vault-mcp.example.toml`](md-vault-mcp.example.toml).

| Key               | Default   | Meaning                                             |
| ----------------- | --------- | --------------------------------------------------- |
| `vault`           | —         | Vault directory.                                    |
| `state_dir`       | —         | Where the journal and its key live.                 |
| `database_url`    | —         | Postgres connection string; the role must own the database (migrations create a text search configuration). |
| `edit_window`     | `48h`     | Go duration.                                        |
| `max_file_size`   | `1048576` | Bytes; larger notes are neither read nor written.   |
| `extensions`      | `[".md"]` |                                                     |
| `transport`       | `stdio`   | `stdio` or `http`.                                  |
| `http.listen`     | —         | e.g. `127.0.0.1:8093`.                              |
| `http.token_file` | —         | File with the bearer token, at least 16 characters. |
| `review.listen`   | —         | Address of the review page, e.g. `127.0.0.1:8193`. |
| `review.public_url` | —       | Address people open; proposal links and the OIDC redirect (`/callback`) use it. |
| `review.allowed_groups` | —   | Groups from the `groups` claim that may apply or reject. |
| `review.oidc_issuer` | —      | OIDC issuer URL. |
| `review.oidc_client_id` | —   | OIDC client id. |
| `review.oidc_client_secret_file` | — | File with the client secret. |

Flags `-transport`, `-listen` and `-token-file` override the config.

```sh
md-vault-mcp -config md-vault-mcp.toml                     # stdio
md-vault-mcp -config md-vault-mcp.toml -transport http     # HTTP
```

An HTTP client must send `Authorization: Bearer <token>` with every request.

## Writes

Writes are atomic: a temporary file in the same directory, then a rename. The
file keeps its mode and owner (if the owner cannot be kept, the write fails),
and the write is refused if the file changed on disk since it was read. Run the
server as the vault's owner — for a Syncthing-synced vault, the Syncthing user.

## NixOS

```nix
{
  inputs.md-vault-mcp.url = "github:pltanton/md-vault-mcp";

  outputs = {nixpkgs, md-vault-mcp, ...}: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      modules = [
        md-vault-mcp.nixosModules.default
        {
          services.md-vault-mcp.instances = {
            personal = {
              user = "syncthing";
              group = "syncthing";
              vault = "/var/lib/syncthing/notes";
              listen = "127.0.0.1:8093";
              tokenFile = "/run/secrets/md-vault-mcp-token";
              read.allow = ["Inbox/**" "Personal/**" "*.md"];
              read.deny = ["**/Work/**"];
              write.allow = ["Inbox/**" "Personal/**"];
            };
            family = {
              user = "syncthing";
              group = "syncthing";
              vault = "/var/lib/syncthing/family";
              listen = "127.0.0.1:8094";
              tokenFile = "/run/secrets/md-vault-mcp-family-token";
              read.mode = "denylist";
              write.mode = "denylist";
            };
          };
        }
      ];
    };
  };
}
```

Each instance is a hardened systemd service `md-vault-mcp-<name>` serving one
vault over HTTP. The token goes through `LoadCredential` (so the token file can
be root-only), and the journal lives in `/var/lib/<stateDirectory>`, by default
`/var/lib/md-vault-mcp-<name>`. Give each instance its own token: a client
holding one cannot reach the other vault.

By default each instance gets a local Postgres database and role
`md_vault_mcp_<name>` (dashes become underscores), reached over the unix socket
with peer authentication mapped from the instance's `user`, so no password
exists and one instance cannot open another's database. The module enables
`services.postgresql` and adds the dictionary to its extensions, which restarts
Postgres on the first deploy. To use another server, set
`database.createLocally = false` and `database.url`.

`review.enable` and its options add the review page; the client secret goes
through `LoadCredential` as `oidc`.

## Security model

The server trusts its configuration and the token holder within the rules: it
is meant to limit what an agent can see and change, not to defend against the
vault's owner or anyone with access to the host. It does not protect against
a concurrent writer racing between its check and its rename beyond the
modification-time check. Keep the HTTP listener on loopback or behind TLS.

## Development

```sh
nix develop
go test ./...
```

The dev shell has Postgres with the dictionary; the tests start a throwaway
cluster with `initdb` and skip the database tests when it is not on `PATH`.

## License

MIT
