{
  description = "md-vault-mcp: scoped MCP access to a Markdown (Obsidian) vault";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    ru-stem-fixes = {
      url = "github:pltanton/ru-stem-fixes";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    self,
    nixpkgs,
    ru-stem-fixes,
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin"];
    forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    stemFixes = pkgs: ru-stem-fixes.packages.${pkgs.stdenv.hostPlatform.system}.default;
  in {
    packages = forAllSystems (pkgs: rec {
      md-vault-mcp = pkgs.callPackage ./nix/package.nix {};
      default = md-vault-mcp;
    });

    nixosModules.default = {pkgs, ...}: {
      imports = [./nix/module.nix];
      services.md-vault-mcp.stemFixesPackage = nixpkgs.lib.mkDefault (stemFixes pkgs);
    };

    devShells = forAllSystems (pkgs: {
      default = pkgs.mkShell {
        packages = [
          pkgs.go
          pkgs.gopls
          (pkgs.postgresql.withPackages (_: [(stemFixes pkgs)]))
        ];
      };
    });
  };
}
