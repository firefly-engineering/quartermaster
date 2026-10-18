# The skill bundle as a Claude plugin directory, for `programs.agentSkills.plugins` or
# `programs.claude-code.plugins`. The test and the script the Go port replaces stay out.
{ lib, runCommand }:
runCommand "quartermaster-skills" { } ''
  mkdir -p "$out/skills"
  cp -R ${../.claude-plugin} "$out/.claude-plugin"
  cp -R ${
    lib.fileset.toSource {
      root = ../skills/worklog;
      fileset = lib.fileset.fileFilter (f: f.hasExt "md") ../skills/worklog;
    }
  } "$out/skills/worklog"
''
