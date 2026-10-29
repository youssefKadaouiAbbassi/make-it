// Package allowlist names the paths that make up a catalog. tools/synccatalog builds the embedded
// catalog from them and makeit takes only them from a directory or git catalog. It is a leaf
// package because the catalog package embeds what synccatalog generates.
package allowlist

import "strings"

// Paths lists the catalog's paths: directories end with a slash.
var Paths = []string{
	".claude-plugin/", "plugins/", "templates/", "ci/",
	"settings.baseline.json", "project.settings.example.json", "makeit.json",
}

// Allowed reports whether p, a slash-separated path relative to the catalog root, is a catalog path.
func Allowed(p string) bool {
	for _, a := range Paths {
		if p == a || (strings.HasSuffix(a, "/") && strings.HasPrefix(p, a)) {
			return true
		}
	}
	return false
}
