package browser

// BrowserIntrinsics are the __-prefixed boundary intrinsics bound only by the
// browser runner. The classifier and the runner both read this list so they
// never disagree about what makes a test a browser test.
var BrowserIntrinsics = []string{
	"__navigate",
	"__click",
	"__fill",
	"__waitVisible",
	"__domText",
}

// IsBrowserIntrinsic reports whether name is one of the browser intrinsics.
func IsBrowserIntrinsic(name string) bool {
	for _, n := range BrowserIntrinsics {
		if n == name {
			return true
		}
	}
	return false
}
