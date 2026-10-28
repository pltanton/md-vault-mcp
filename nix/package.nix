{
  lib,
  buildGoModule,
}:
buildGoModule {
  pname = "md-vault-mcp";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
    ];
  };

  vendorHash = "sha256-f9ikpiNy78Pfd3b/eE+kzgxMqD3C1lp+Eypjh4+UfzU=";

  subPackages = ["cmd/md-vault-mcp"];
  ldflags = ["-s" "-w" "-X main.version=0.1.0"];

  meta = {
    description = "MCP server giving an AI agent scoped read and append access to a Markdown vault";
    license = lib.licenses.mit;
    mainProgram = "md-vault-mcp";
  };
}
