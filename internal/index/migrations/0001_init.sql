CREATE TEXT SEARCH DICTIONARY ru_stem_fixes (TEMPLATE = synonym, SYNONYMS = ru_stem_fixes);

CREATE TEXT SEARCH CONFIGURATION md_vault (COPY = russian);

ALTER TEXT SEARCH CONFIGURATION md_vault
  ALTER MAPPING FOR word, hword, hword_part WITH ru_stem_fixes, russian_stem;

CREATE TABLE files (
  path     text PRIMARY KEY,
  mtime_ns bigint NOT NULL,
  size     bigint NOT NULL,
  name_tsv tsvector NOT NULL
);

CREATE TABLE sections (
  path       text NOT NULL REFERENCES files (path) ON DELETE CASCADE,
  start_line int NOT NULL,
  end_line   int NOT NULL,
  tsv        tsvector NOT NULL,
  PRIMARY KEY (path, start_line)
);

CREATE INDEX sections_tsv ON sections USING gin (tsv);
CREATE INDEX files_name_tsv ON files USING gin (name_tsv);
