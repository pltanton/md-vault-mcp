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

  stateDir = "/var/lib/md-vault-mcp";

  configFile = toml.generate "md-vault-mcp.toml" ({
      vault = cfg.vault;
      state_dir = stateDir;
      edit_window = cfg.editWindow;
      max_file_size = cfg.maxFileSize;
      extensions = cfg.extensions;
      read = ruleSettings cfg.read;
      write = ruleSettings cfg.write;
      transport = "http";
      http = {
        listen = cfg.listen;
        token_file = "/run/credentials/md-vault-mcp.service/token";
      };
    }
    // cfg.extraSettings);
in {
  options.services.md-vault-mcp = {
    enable = lib.mkEnableOption "md-vault-mcp, scoped MCP access to a Markdown vault over HTTP";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix {};
      defaultText = lib.literalExpression "pkgs.callPackage ./nix/package.nix {}";
    };

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
      default = "127.0.0.1:8093";
    };

    tokenFile = lib.mkOption {
      type = lib.types.str;
      description = ''
        Runtime path of a file holding the bearer token (at least 16
        characters). Passed via systemd LoadCredential, so it may be readable
        by root only. Not a Nix path: that would copy the secret into the store.
      '';
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

  config = lib.mkIf cfg.enable {
    users.users = lib.mkIf (cfg.user == "md-vault-mcp") {
      md-vault-mcp = {
        isSystemUser = true;
        group = cfg.group;
      };
    };
    users.groups = lib.mkIf (cfg.group == "md-vault-mcp") {md-vault-mcp = {};};

    systemd.services.md-vault-mcp = {
      description = "md-vault-mcp, scoped MCP access to a Markdown vault";
      wantedBy = ["multi-user.target"];
      after = ["network.target"];

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} -config ${configFile}";
        User = cfg.user;
        Group = cfg.group;
        LoadCredential = ["token:${cfg.tokenFile}"];
        StateDirectory = "md-vault-mcp";
        StateDirectoryMode = "0700";
        Restart = "on-failure";
        RestartSec = "5s";

        ProtectSystem = "strict";
        ProtectHome = "read-only";
        ReadWritePaths = [cfg.vault];
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
  };
}
