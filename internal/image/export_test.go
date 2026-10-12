package image

// SetPartSize changes how big BuildForPublishing's parts are, until the
// returned function is called.
func SetPartSize(n int64) (restore func()) {
	old := partSize
	partSize = n
	return func() { partSize = old }
}
