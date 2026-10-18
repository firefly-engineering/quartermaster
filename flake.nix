{
  description = "quartermaster: a suite of agent tools, starting with the worklog note and its agent dispatch as an MCP server";

  nixConfig = {
    extra-substituters = [ "https://firefly-toolbox.cachix.org" ];
    extra-trusted-public-keys = [
      "firefly-toolbox.cachix.org-1:4RgCoc0+CS7QhRarG109VmWlnlYi+rQ5JYrCsRP5aK8="
    ];
  };

  inputs = {
    nix-pins.url = "github:firefly-engineering/nix-pins";
    nixpkgs.follows = "nix-pins/nixpkgs";
    toolbox.url = "github:firefly-engineering/toolbox";
    toolbox.inputs.nix-pins.follows = "nix-pins";
    toolbox.inputs.devenv.follows = "";
  };

  outputs =
    {
      self,
      nixpkgs,
      nix-pins,
      toolbox,
      ...
    }:
    let
      # The systems toolbox builds for; it does not ship x86_64-darwin.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems f;

      # One go-toolchain version for the shell and the package: the package's Go is read from
      # that version's pin map, so the two cannot disagree. Pick a version whose golangci-lint is
      # built with its own Go minor: toolbox does not enforce that yet, and a mismatch fails lint
      # on Linux (https://github.com/firefly-engineering/toolbox/issues/7).
      goToolchainVersion = "7";
      goToolchainFor = system: toolbox.registry.${system}.go-toolchain.versions.${goToolchainVersion};
      goFor =
        system:
        let
          reg = toolbox.registry.${system};
        in
        reg.go.versions.${reg.go-toolchain.toolbox.components.${goToolchainVersion}.go};
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        rec {
          worklog = pkgs.callPackage ./nix/package.nix { go = goFor system; };
          skills = pkgs.callPackage ./nix/skills.nix { inherit (worklog) version; };
          default = worklog;
        }
      );

      overlays.default = final: _: {
        worklog = self.packages.${final.stdenv.hostPlatform.system}.worklog;
      };

      homeManagerModules.default = import ./nix/module.nix { inherit (self) packages; };

      checks = forAllSystems (
        system:
        import ./nix/checks.nix {
          inherit self nixpkgs system;
          inherit (nix-pins.inputs) home-manager;
        }
      );

      formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt-tree);

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          tb = toolbox.packages.${system};
        in
        {
          default = pkgs.mkShellNoCC {
            packages = [
              (goToolchainFor system)
              tb.just
              tb.nix-toolchain
              tb.vcs-toolchain
            ];
          };
        }
      );
    };
}
