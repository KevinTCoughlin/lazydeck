{
  description = "LazyDeck: a terminal UI for Steam devkit fleets";

  # LazyDeck follows Go's supported release line, so use nixpkgs-unstable
  # rather than a stable channel whose Go compiler can lag the module's
  # declared minimum version. flake.lock pins the exact revision.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.buildGoModule {
            pname = "lazydeck";
            version = "unstable-${self.shortRev or "dirty"}";
            src = self;
            vendorHash = "sha256-atyAbVzcnQpbbXhFN6iy27HuXW8Zsur4gm4IrSSw8VU=";
            env.CGO_ENABLED = 0;
            # CI runs the complete suite. The Go client cancellation test
            # requires process-group reaping that Nix's build sandbox cannot
            # reliably provide, so avoid making installation depend on that
            # sandbox-specific behavior.
            doCheck = false;

            ldflags = [
              "-s"
              "-w"
              "-X main.version=unstable-${self.shortRev or "dirty"}"
              "-X main.commit=${self.rev or "dirty"}"
              "-X main.builtBy=nix"
            ];

            nativeBuildInputs = [ pkgs.makeWrapper ];

            postInstall = ''
              install -Dm644 docs/lazydeck.1 "$out/share/man/man1/lazydeck.1"
              install -Dm644 completions/lazydeck.bash \
                "$out/share/bash-completion/completions/lazydeck"
              install -Dm644 completions/_lazydeck \
                "$out/share/zsh/site-functions/_lazydeck"
              install -Dm644 completions/lazydeck.fish \
                "$out/share/fish/vendor_completions.d/lazydeck.fish"
              mkdir -p "$out/share/lazydeck"
              cp -r python "$out/share/lazydeck/python"
              rm -rf "$out/share/lazydeck/python/.venv"
              wrapProgram "$out/bin/lazydeck" \
                --set LAZYDECK_PYTHON_DIR "$out/share/lazydeck/python" \
                --prefix PATH : "${pkgs.lib.makeBinPath [ pkgs.uv pkgs.openssh pkgs.rsync ]}"
            '';

            meta = {
              description = "Terminal UI for managing Steam devkit fleets";
              homepage = "https://github.com/KevinTCoughlin/lazydeck";
              license = pkgs.lib.licenses.mit;
              mainProgram = "lazydeck";
              platforms = systems;
            };
          };
        });

      devShells = forAllSystems (system:
        let pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go
              uv
              just
              gopls
              golangci-lint
              ruff
              shellcheck
            ];
          };
        });
    };
}
