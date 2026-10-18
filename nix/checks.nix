# The package builds with its tests, the skill bundle builds, and the home-manager module
# installs the binary and emits config.toml.
{
  self,
  nixpkgs,
  home-manager,
  system,
}:
let
  pkgs = nixpkgs.legacyPackages.${system};
  qm = self.packages.${system};
  home = home-manager.lib.homeManagerConfiguration {
    inherit pkgs;
    modules = [
      self.homeManagerModules.default
      {
        home = {
          username = "check";
          homeDirectory = "/home/check";
          stateVersion = "25.11";
        };
        # nix-pins pairs its own nixpkgs and home-manager; the release check is theirs to keep.
        home.enableNixpkgsReleaseCheck = false;
        programs.quartermaster = {
          enable = true;
          settings.vault.repo = "/vault";
        };
      }
    ];
  };
  inherit (home) config;
in
assert builtins.elem qm.quartermaster config.home.packages;
{
  package = qm.quartermaster;
  skills = qm.skills;
  module = pkgs.runCommand "quartermaster-module-check" { } ''
    grep -qFx 'repo = "/vault"' ${config.xdg.configFile."quartermaster/config.toml".source}
    touch "$out"
  '';
}
