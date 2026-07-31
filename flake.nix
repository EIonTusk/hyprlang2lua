{
  description = "Convert legacy Hyprland hyprlang configs to the Hyprland 0.55+ Lua format";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      # Limit to the systems where Hyprland is realistically consumed.
      # Hyprland itself is Linux-only, but the converter is pure-Go and
      # runs anywhere a Go toolchain does, so darwin is included for
      # contributors editing the converter on a Mac.
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];

      forAllSystems = f: nixpkgs.lib.genAttrs systems
        (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (system: pkgs: rec {
        default = hyprlang2lua;

        hyprlang2lua = pkgs.buildGoModule {
          pname = "hyprlang2lua";
          # Keep this in sync with the git tag the AUR PKGBUILD points at.
          # `nix flake update` will re-fetch nixpkgs but won't change this.
          version = "0.7.1";

          src = self;

          # Hash of the vendored Go module cache. Only changes when go.mod
          # or go.sum changes (a dependency added/removed/bumped). If Nix
          # complains about a mismatch after one of those edits, replace
          # this string with the `got: sha256-…` value it prints.
          vendorHash = "sha256-Z8V1a3uJdG/lj6AP4Xly01MQSq/yBnB2/TuERrrj0o0=";

          subPackages = [ "cmd/hyprlang2lua" ];

          ldflags = [ "-s" "-w" ];

          # The converter package is filesystem-free; the test suite reads
          # testdata/*.conf / *.lua goldens, all under src/. doCheck=true
          # runs the full Go suite (golden + smoke + race-free) in the
          # Nix sandbox. The golden test's optional `luac -p` step is a
          # no-op when lua isn't in the sandbox, so we don't add it as a
          # build dep — keeps the closure small. Set checkInputs to add
          # lua if you want that extra gate locally.
          doCheck = true;

          meta = with pkgs.lib; {
            description = "Convert legacy Hyprland hyprlang configs to the Hyprland 0.55+ Lua format";
            homepage = "https://github.com/EIonTusk/hyprlang2lua";
            license = licenses.mit;
            mainProgram = "hyprlang2lua";
            platforms = platforms.unix;
          };
        };
      });

      # `nix run github:EIonTusk/hyprlang2lua -- input.conf > output.lua`
      apps = forAllSystems (system: pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/hyprlang2lua";
        };
      });

      # `nix develop` — Go toolchain plus Lua for the optional `luac -p`
      # syntax gate that golden_test.go runs on every generated .lua file.
      devShells = forAllSystems (system: pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            lua
          ];
        };
      });

      # Lets `nix flake check` verify the package builds without `nix build`.
      checks = forAllSystems (system: pkgs: {
        default = self.packages.${system}.default;
      });

      # Optional: `nix fmt` on this flake.
      formatter = forAllSystems (system: pkgs: pkgs.nixpkgs-fmt);
    };
}
