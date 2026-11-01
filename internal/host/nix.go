package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func nixBin() (string, error) {
	p, ok := lookPath("nix", "/nix/var/nix/profiles/default/bin/nix")
	if !ok {
		return "", fmt.Errorf("nix not found; run `gci bootstrap`")
	}
	return p, nil
}

// nixExpr returns a buildEnv expression combining the given package expressions.
func nixExpr(nixpkgs, name string, packages []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "let pkgs = (builtins.getFlake %q).legacyPackages.${builtins.currentSystem}; in\n", nixpkgs)
	fmt.Fprintf(&b, "with pkgs; buildEnv { name = %q; paths = [\n", name)
	for _, p := range packages {
		fmt.Fprintf(&b, "  (%s)\n", p)
	}
	b.WriteString("]; ignoreCollisions = true; }\n")
	return b.String()
}

// buildEnv realises the package set and registers outLink as a GC root.
// Paths already in the local store (or cached) make this a fast no-op.
func buildEnv(ctx context.Context, nixpkgs, name string, packages []string, outLink string) (string, error) {
	nix, err := nixBin()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outLink), 0o755); err != nil {
		return "", err
	}
	_, err = run(ctx, nix, "--extra-experimental-features", "nix-command flakes",
		"build", "--impure", "--out-link", outLink, "--expr", nixExpr(nixpkgs, name, packages))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(outLink)
}
