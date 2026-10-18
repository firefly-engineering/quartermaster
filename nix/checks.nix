# The package builds with its tests, the skill bundle builds with the package's version and its
# manifest lists every skill under skills/, and the home-manager module installs the binary and
# emits quartermaster/worklog.toml.
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
        programs.quartermaster.worklog = {
          enable = true;
          settings.vault.repo = "/vault";
        };
      }
    ];
  };
  inherit (home) config;
in
assert builtins.elem qm.worklog config.home.packages;
{
  package = qm.worklog;
  skills = pkgs.runCommand "quartermaster-skills-check" { nativeBuildInputs = [ pkgs.jq ]; } ''
    jq -e --arg v ${qm.worklog.version} '.version == $v' \
      ${qm.skills}/.claude-plugin/plugin.json
    for dir in ${../skills}/*/; do
      skill=$(basename "$dir")
      jq -e --arg s "./skills/$skill" 'any(.skills[]; . == $s)' \
        ${qm.skills}/.claude-plugin/plugin.json
      test -f ${qm.skills}/skills/$skill/SKILL.md
    done
    test -x ${qm.skills}/skills/worktree-build-cache/scripts/bench-cache.sh
    touch "$out"
  '';
  module = pkgs.runCommand "worklog-module-check" { } ''
    grep -qFx 'repo = "/vault"' ${config.xdg.configFile."quartermaster/worklog.toml".source}
    touch "$out"
  '';
}
