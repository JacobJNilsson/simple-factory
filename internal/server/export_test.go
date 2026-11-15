package server

// SetMaxLine changes the request size limit and returns a function that restores it.
func SetMaxLine(n int) (restore func()) {
	old := maxLine
	maxLine = n
	return func() { maxLine = old }
}
