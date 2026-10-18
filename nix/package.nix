{
  lib,
  buildGoModule,
  go,
}:
(buildGoModule.override { inherit go; }) (finalAttrs: {
  pname = "worklog";
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

  vendorHash = "sha256-Hh4eKyRJmwrRkc0uCLqjAFECo36aicrnjpbCF2E1F7k=";
  subPackages = [ "cmd/worklog" ];
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
    mainProgram = "worklog";
  };
})
