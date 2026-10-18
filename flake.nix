{
  description = "quartermaster: the worklog note and its agent dispatch, as an MCP server";

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

      # The package builds with the Go of the dev shell's go-toolchain, read from toolbox's own
      # pin map, so the two cannot disagree.
      goFor =
        system:
        let
          reg = toolbox.registry.${system};
          tc = reg.go-toolchain;
        in
        reg.go.versions.${tc.toolbox.components.${tc.default}.go};
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        rec {
          quartermaster = pkgs.callPackage ./nix/package.nix { go = goFor system; };
          skills = pkgs.callPackage ./nix/skills.nix { };
          default = quartermaster;
        }
      );

      overlays.default = final: _: {
        quartermaster = self.packages.${final.stdenv.hostPlatform.system}.quartermaster;
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
              tb.go-toolchain
              tb.just
              tb.nix-toolchain
              tb.vcs-toolchain
            ];
          };
        }
      );
    };
}
