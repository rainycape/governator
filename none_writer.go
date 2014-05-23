package main

type noneWriter struct{}

func (w *noneWriter) Open(_ string) error {
	return nil
}

func (w *noneWriter) Close() error {
	return nil
}

func (w *noneWriter) Write(_ string, _ []byte) error {
	return nil
}

func (w *noneWriter) Flush() error {
	return nil
}
