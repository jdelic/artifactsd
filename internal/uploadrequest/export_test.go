package uploadrequest

// SetBeforeCommitHook installs f to run between the receive and commit phases
// of an upload (tests only) and returns a function restoring the previous hook.
func SetBeforeCommitHook(f func()) (restore func()) {
	prev := beforeCommitHook
	beforeCommitHook = f
	return func() { beforeCommitHook = prev }
}
