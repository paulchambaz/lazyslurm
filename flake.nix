{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};

      buildPkgs = with pkgs; [
        scdoc
      ];

      devPkgs = with pkgs; [
        go
        gopls
        just
      ];
    in {
      packages.default = pkgs.buildGoModule {
        pname = "lazyslurm";
        version = pkgs.lib.removeSuffix "\n" (builtins.readFile ./src/VERSION);
        src = ./.;
        vendorHash = "sha256-phS6b2DODwgXh68xECQMLNS4FwjtYPuX4G4XJcJi4oY=";
        subPackages = ["src"];

        nativeBuildInputs = buildPkgs;
        nativeCheckInputs = with pkgs; [git rsync openssh util-linux];

        postInstall = ''
          mv $out/bin/src $out/bin/lazyslurm
          mkdir -p $out/share/man/man1
          SOURCE_DATE_EPOCH=${toString self.lastModified} scdoc < lazyslurm.1.scd > $out/share/man/man1/lazyslurm.1
        '';

        meta = {
          description = "A lazygit-like terminal UI for Slurm clusters";
          homepage = "https://github.com/paulchambaz/lazyslurm";
          license = pkgs.lib.licenses.gpl3Only;
          mainProgram = "lazyslurm";
        };
      };

      devShells.default = pkgs.mkShell {
        nativeBuildInputs = buildPkgs;
        buildInputs = devPkgs;
        shellHook = ''
          git config core.hooksPath .githooks 2>/dev/null || true
        '';
      };
    });
}
