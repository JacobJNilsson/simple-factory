package engine_test

// fakeRegistration is a registration that the test sets.
type fakeRegistration struct {
	registered bool
}

func (f *fakeRegistration) Registered() bool { return f.registered }
