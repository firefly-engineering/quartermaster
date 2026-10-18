# The skill bundle as a Claude plugin directory, for `programs.agentSkills.plugins` or
# `programs.claude-code.plugins`. The manifest's version is the package's, written here so the
# checked-in plugin.json carries no second copy of it.
{
  lib,
  runCommand,
  writeText,
  version,
}:
let
  manifest = writeText "plugin.json" (
    builtins.toJSON (lib.importJSON ../.claude-plugin/plugin.json // { inherit version; })
  );
in
runCommand "quartermaster-skills-${version}" { } ''
  mkdir -p "$out/.claude-plugin" "$out/skills"
  cp ${manifest} "$out/.claude-plugin/plugin.json"
  cp -R ${../skills/worklog} "$out/skills/worklog"
''
