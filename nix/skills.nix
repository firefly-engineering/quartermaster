# The skill bundle as a Claude plugin directory, for `programs.agentSkills.plugins` or
# `programs.claude-code.plugins`. The manifest's version is the package's, written here so the
# checked-in plugin.json carries no second copy of it. The manifest's `skills` list is the one
# list of skills: each entry is copied from the repo.
{
  lib,
  runCommand,
  writeText,
  version,
}:
let
  plugin = lib.importJSON ../.claude-plugin/plugin.json;
  manifest = writeText "plugin.json" (builtins.toJSON (plugin // { inherit version; }));
in
runCommand "quartermaster-skills-${version}" { } ''
  mkdir -p "$out/.claude-plugin" "$out/skills"
  cp ${manifest} "$out/.claude-plugin/plugin.json"
  ${lib.concatMapStrings (s: ''
    cp -R ${../. + "/${s}"} "$out/${s}"
  '') plugin.skills}''
