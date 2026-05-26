{
  description = "Claude Code PreToolUse hook: auto-allow strictly-whitelisted read-only Bash commands";

  inputs = {
    # Pin to nixos-25.11 so standalone `nix build` works. A consumer flake can override
    # it via `inputs.classify-bash.inputs.nixpkgs.follows = "nixpkgs"`,
    # sharing a single nixpkgs evaluation.
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = import nixpkgs {inherit system;};

      classify-bash = pkgs.buildGoModule {
        pname = "classify-bash";
        version = "0.1.0";
        src = ./.;
        # Filled in after first build attempt. `nix build` will print the
        # correct hash on the initial failure; paste it here.
        vendorHash = "sha256-DQS+zZEOz9aJfwZt6Fq5T7Hm1JQK6AJ3IqJqMzC27rU=";
        meta = {
          description = "Claude Code Bash classifier (strict read-only whitelist)";
          mainProgram = "classify-bash";
        };
      };
    in {
      packages = {
        default = classify-bash;
        classify-bash = classify-bash;
      };

      # `nix flake check` runs the Go test corpus (TestMustAllow,
      # TestMustNotAllow, TestEventDecode*). Failure means the classifier
      # regressed in a way that affects safety.
      checks.tests = pkgs.runCommand "classify-bash-tests" {
        nativeBuildInputs = [pkgs.go];
        src = ./.;
      } ''
        # Go refuses to build from /build (TMPDIR root). Use a subdir.
        mkdir -p work && cd work
        cp -r $src/. .
        chmod -R +w .
        export HOME=$TMPDIR
        export GOCACHE=$TMPDIR/go-cache
        export GOFLAGS=-mod=vendor
        # buildGoModule's goModules output IS the vendor directory.
        cp -r ${classify-bash.goModules} ./vendor
        chmod -R +w ./vendor
        go test ./...
        touch $out
      '';

      # `nix develop` enters a shell with Go and friends for iterating on the
      # classifier: `go mod tidy`, `go test ./...`, `go run . <<<'...'` etc.
      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          gotools
          go-tools
          delve
        ];
        shellHook = ''
          echo "classify-bash dev shell. go test ./... to run the corpus."
        '';
      };
    });
}
