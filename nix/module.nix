# programs.quartermaster for home-manager (spec §10.1): the binary, and `settings` projected
# into config.toml. Installing the skill and registering the MCP server are left to the user's
# own setup.
{ packages }:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.quartermaster;
  toml = pkgs.formats.toml { };
in
{
  options.programs.quartermaster = {
    enable = lib.mkEnableOption "quartermaster";

    package = lib.mkOption {
      type = lib.types.package;
      default = packages.${pkgs.stdenv.hostPlatform.system}.quartermaster;
      defaultText = lib.literalMD "quartermaster, from this flake";
      description = "The quartermaster binary.";
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
      description = "config.toml, key for key (spec §3).";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
    xdg.configFile."quartermaster/config.toml" = lib.mkIf (cfg.settings != { }) {
      source = toml.generate "quartermaster-config.toml" cfg.settings;
    };
  };
}
