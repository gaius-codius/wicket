package config

import "fmt"

// LoadError is a fatal config-file problem. The file bytes must not be mutated.
type LoadError struct {
	Path string
	Err  error
}

func (e *LoadError) Error() string {
	if e == nil {
		return "load error"
	}
	if e.Path == "" {
		if e.Err == nil {
			return "load error"
		}
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %v", e.Path, e.Err)
}

func (e *LoadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func loadError(path string, err error) error {
	if err == nil {
		return nil
	}
	var le *LoadError
	if errorsAs(err, &le) {
		return err
	}
	return &LoadError{Path: path, Err: err}
}

func errorsAs(err error, target **LoadError) bool {
	le, ok := err.(*LoadError)
	if !ok {
		return false
	}
	*target = le
	return true
}
