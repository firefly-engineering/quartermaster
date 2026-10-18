# Button setup (once per vault)

The ▶ buttons need the community **Shell commands** plugin
([docs](https://publish.obsidian.md/shellcommands)) and one command that runs
`quartermaster send`.

1. Turn on the plugin. Go to Settings → Community plugins, then install and enable
   **Shell commands**.
2. **Obsidian 1.13 or later:** the plugin's settings tabs are blank. This is
   [Taitava/obsidian-shellcommands#483](https://github.com/Taitava/obsidian-shellcommands/issues/483),
   and no release fixes it yet. Work around it with a CSS snippet:
   - put `.obsidian/snippets/shellcommands-tabs.css` in the vault, containing
     `.SC-tab-content { display: block; }`;
   - enable it in Settings → Appearance → CSS snippets. Reload the list if the snippet
     doesn't appear.

   Every tab's content then shows on one page, stacked. Delete the snippet once the bug
   is fixed in a release.
3. Create the custom variables `_date` and `_item`. Go to the plugin's settings, find the
   *Variables* section, and use *New custom variable*. If the name field already shows
   the `_` prefix, type only `date` and `item`.
4. Add the shell command. Go to the *Shell commands* section, use *New shell command*,
   and enter:

   ```sh
   PATH="/etc/profiles/per-user/$USER/bin:$PATH" quartermaster send {{vault_path}} {{_date}} {{_item}}
   ```

   Obsidian started from the Dock does not have the nix profile on `PATH`, so the
   prefix is what finds `quartermaster` and lets it find `herdr`. No herdr environment variables are
   needed: `herdr` reaches the running session on its own.
5. Pick an output channel. A notification works well: it shows `sent <id> to <workspace>`,
   or the reason it refused.

quartermaster finds the command's id by itself, in
`.obsidian/plugins/obsidian-shellcommands/data.json`. Once the command exists, every work
item with a prompt that it writes carries its button, in its bucket and in *Next*.

## Why the URI carries only a reference

The plugin runs a URI's command with no confirmation, and its own docs say its variable
escaping "cannot guarantee the escaping to be 100% secure". Any web page or email can
open an `obsidian://` link.

So `quartermaster send` accepts only a date and an id, and validates both. It reads the prompt
from your own note, and passes it to `herdr` as an argument, never through a shell. A
forged link can only resend a prompt you already wrote.

It also refuses to type into an agent that is `blocked`, because there the text
could answer an approval prompt.
