//go:build !unix

package mediaserver

// linkIdentities reports nothing off Unix, where a file has no uid to compare.
func linkIdentities(string) (fileOwner, linker, bool) {
	return fileOwner{}, linker{}, false
}
