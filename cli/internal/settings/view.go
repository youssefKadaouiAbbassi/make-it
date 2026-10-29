package settings

import "makeit/internal/jsonx"

// Show renders a settings value for a message or a plan; a secret is never printed.
func Show(path string, v any) string {
	if IsSecret(path) {
		return "(secret)"
	}
	b, err := jsonx.Compact(v)
	if err != nil {
		return "(unprintable)"
	}
	return string(b)
}

// Desired is the baseline as it applies to this machine. shortcut: the catalog is embedded, so
// its marketplace is a directory one, added by claude plugin marketplace add, which never
// auto-updates; the baseline's entry for it is for a git catalog and is dropped, as
// tools/applysettings does. Keep it when --catalog <git url> lands.
func Desired(baseline *jsonx.Object, ownMarketplace string) *jsonx.Object {
	if mk, ok := baseline.Get("extraKnownMarketplaces"); ok {
		if o, ok := mk.(*jsonx.Object); ok {
			o.Delete(ownMarketplace)
		}
	}
	return baseline
}
