package task

// Prepared is what a worker start fixes for a task: its folder, its branch and its lease.
type Prepared struct {
	Dir    string
	Branch string
	Lease  string
}
