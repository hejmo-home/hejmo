package picture

// Hold takes the decode slot the way a running Process does and returns the
// function that gives it back.
func Hold() (release func()) {
	one <- struct{}{}
	return func() { <-one }
}
