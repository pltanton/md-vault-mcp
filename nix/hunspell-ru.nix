{
  lib,
  runCommand,
  hunspellDicts,
  glibc,
}:
runCommand "md-vault-mcp-hunspell-ru" {
  nativeBuildInputs = [glibc.bin];
  meta = {
    description = "Russian hunspell dictionary as a Postgres text search dictionary (md_vault_ru)";
    platforms = lib.platforms.linux;
  };
} ''
  d=$out/share/postgresql/tsearch_data
  mkdir -p $d
  src=${hunspellDicts.ru_RU}/share/hunspell
  iconv -f KOI8-R -t UTF-8 $src/ru_RU.aff | sed '1s/^SET KOI8-R/SET UTF-8/' > $d/md_vault_ru.affix
  iconv -f KOI8-R -t UTF-8 $src/ru_RU.dic > $d/md_vault_ru.dict
''
