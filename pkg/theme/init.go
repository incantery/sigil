package theme

// init validates the bundled default themes at package load. If a future
// maintainer ships a default theme whose tokens fail WCAG AA, every Sigil
// binary refuses to start — the design system is broken at the foundation
// and we'd rather not boot than silently ship inaccessible UI.
func init() {
	Light.MustValidate()
	Dark.MustValidate()
	HighContrast.Apply(Light).MustValidate()
	DarkHighContrast.Apply(Dark).MustValidate()
}
