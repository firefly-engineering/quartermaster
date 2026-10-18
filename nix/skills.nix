# The skill bundle as a Claude plugin directory, for `programs.agentSkills.plugins` or
# `programs.claude-code.plugins`.
{ runCommand }:
runCommand "quartermaster-skills" { } ''
  mkdir -p "$out/skills"
  cp -R ${../.claude-plugin} "$out/.claude-plugin"
  cp -R ${../skills/worklog} "$out/skills/worklog"
''
