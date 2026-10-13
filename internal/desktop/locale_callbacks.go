package desktop

func (d *desktop) guardValue(action func(string)) func(string) {
	return func(value string) { d.guard(func() { action(value) })() }
}
