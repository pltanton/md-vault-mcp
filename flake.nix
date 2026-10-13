{
  description = "md-vault-mcp: scoped MCP access to a Markdown (Obsidian) vault";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = {
    self,
    nixpkgs,
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin"];
    forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
  in {
    packages = forAllSystems (pkgs: rec {
      md-vault-mcp = pkgs.callPackage ./nix/package.nix {};
      default = md-vault-mcp;
    });

    nixosModules.default = import ./nix/module.nix;

    devShells = forAllSystems (pkgs: {
      default = pkgs.mkShell {packages = [pkgs.go pkgs.gopls];};
    });
  };
}
