{
  lib,
  buildGoModule,
  go,
}:
(buildGoModule.override { inherit go; }) (finalAttrs: {
  pname = "quartermaster";
  version = "0.0.0";

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
      ../skills
    ];
  };

  vendorHash = "sha256-pRmtJsS55ntxO0Hs3Ig2EPTB/pnYN5dVkUZ7MW7Zhps=";
  subPackages = [ "cmd/quartermaster" ];
  env.CGO_ENABLED = "0";
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${finalAttrs.version}"
  ];

  meta = {
    description = "The worklog note and its agent dispatch, as an MCP server";
    homepage = "https://github.com/firefly-engineering/quartermaster";
    license = lib.licenses.mit;
    mainProgram = "quartermaster";
  };
})
