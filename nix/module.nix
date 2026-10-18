# programs.quartermaster.worklog for home-manager (spec §10.1): the worklog binary, and
# `settings` projected into quartermaster/worklog.toml. Installing the skill and registering the
# MCP server are left to the user's own setup.
{ packages }:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.quartermaster.worklog;
  toml = pkgs.formats.toml { };
in
{
  options.programs.quartermaster.worklog = {
    enable = lib.mkEnableOption "worklog";

    package = lib.mkOption {
      type = lib.types.package;
      default = packages.${pkgs.stdenv.hostPlatform.system}.worklog;
      defaultText = lib.literalMD "worklog, from this flake";
      description = "The worklog binary.";
    };

    settings = lib.mkOption {
      inherit (toml) type;
      default = { };
      example = lib.literalExpression ''
        {
          vault.repo = "~/src/github.com/sigma/worklog";
          projects."arbora-partners/trunk" = {
            path = "~/src/github.com/arbora-partners/trunk";
            workspace = "trunk";
          };
        }
      '';
      description = "worklog.toml, key for key (spec §3).";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
    xdg.configFile."quartermaster/worklog.toml" = lib.mkIf (cfg.settings != { }) {
      source = toml.generate "worklog.toml" cfg.settings;
    };
  };
}
