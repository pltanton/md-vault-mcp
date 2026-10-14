{
  config,
  lib,
  pkgs,
  ...
}: let
  cfg = config.services.md-vault-mcp;
  toml = pkgs.formats.toml {};

  ruleSetType = lib.types.submodule {
    options = {
      mode = lib.mkOption {
        type = lib.types.enum ["allowlist" "denylist"];
        default = "allowlist";
      };
      allow = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [];
        description = "Globs relative to the vault; only used in allowlist mode.";
      };
      deny = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [];
        description = "Globs relative to the vault; deny always wins.";
      };
    };
  };

  ruleSettings = r:
    {inherit (r) mode;}
    // lib.optionalAttrs (r.allow != []) {inherit (r) allow;}
    // lib.optionalAttrs (r.deny != []) {inherit (r) deny;};

  instanceType = lib.types.submodule ({name, ...}: {
    options = {
      user = lib.mkOption {
        type = lib.types.str;
        default = "md-vault-mcp";
        description = "User the server runs as. Use the vault's owner so written files keep their ownership.";
      };

      group = lib.mkOption {
        type = lib.types.str;
        default = "md-vault-mcp";
      };

      vault = lib.mkOption {
        type = lib.types.str;
        description = "Absolute path of the vault directory.";
      };

      listen = lib.mkOption {
        type = lib.types.str;
        example = "127.0.0.1:8093";
      };

      tokenFile = lib.mkOption {
        type = lib.types.str;
        description = ''
          Runtime path of a file holding the bearer token (at least 16
          characters). Passed via systemd LoadCredential, so it may be readable
          by root only. Not a Nix path: that would copy the secret into the store.
        '';
      };

      stateDirectory = lib.mkOption {
        type = lib.types.str;
        default = "md-vault-mcp-${name}";
        description = "Directory under /var/lib for the journal of the agent's own edits.";
      };

      editWindow = lib.mkOption {
        type = lib.types.str;
        default = "48h";
        description = "How long after writing a block the agent may still change or remove it.";
      };

      maxFileSize = lib.mkOption {
        type = lib.types.ints.positive;
        default = 1048576;
      };

      extensions = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [".md"];
      };

      read = lib.mkOption {
        type = ruleSetType;
        default = {};
      };

      write = lib.mkOption {
        type = ruleSetType;
        default = {};
        description = "Write rules; always intersected with the read rules.";
      };

      extraSettings = lib.mkOption {
        type = toml.type;
        default = {};
      };
    };
  });

  service = name: inst: let
    configFile = toml.generate "md-vault-mcp-${name}.toml" ({
        inherit (inst) vault extensions;
        state_dir = "/var/lib/${inst.stateDirectory}";
        edit_window = inst.editWindow;
        max_file_size = inst.maxFileSize;
        read = ruleSettings inst.read;
        write = ruleSettings inst.write;
        transport = "http";
        http = {
          inherit (inst) listen;
          token_file = "/run/credentials/md-vault-mcp-${name}.service/token";
        };
      }
      // inst.extraSettings);
  in
    lib.nameValuePair "md-vault-mcp-${name}" {
      description = "md-vault-mcp ${name}, scoped MCP access to a Markdown vault";
      wantedBy = ["multi-user.target"];
      after = ["network.target"];

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} -config ${configFile}";
        User = inst.user;
        Group = inst.group;
        LoadCredential = ["token:${inst.tokenFile}"];
        StateDirectory = inst.stateDirectory;
        StateDirectoryMode = "0700";
        Restart = "on-failure";
        RestartSec = "5s";

        ProtectSystem = "strict";
        ProtectHome = "read-only";
        ReadWritePaths = [inst.vault];
        PrivateTmp = true;
        PrivateDevices = true;
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        RestrictAddressFamilies = ["AF_INET" "AF_INET6" "AF_UNIX"];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = ["@system-service"];
        UMask = "0022";
      };
    };

  usesDefault = attr: lib.any (inst: inst.${attr} == "md-vault-mcp") (lib.attrValues cfg.instances);
in {
  options.services.md-vault-mcp = {
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix {};
      defaultText = lib.literalExpression "pkgs.callPackage ./nix/package.nix {}";
    };

    instances = lib.mkOption {
      type = lib.types.attrsOf instanceType;
      default = {};
      description = "One HTTP server per vault, as the systemd service md-vault-mcp-<name>.";
    };
  };

  config = lib.mkIf (cfg.instances != {}) {
    users.users = lib.mkIf (usesDefault "user") {
      md-vault-mcp = {
        isSystemUser = true;
        group = "md-vault-mcp";
      };
    };
    users.groups = lib.mkIf (usesDefault "group") {md-vault-mcp = {};};

    systemd.services = lib.mapAttrs' service cfg.instances;
  };
}
