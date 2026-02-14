package server

import "fmt"

func DefaultServerName() (string, bool, error) {
	entries, err := load()
	if err != nil {
		return "", false, err
	}

	switch len(entries) {
	case 0:
		return "", false, nil
	case 1:
		return entryName(entries[0]), true, nil
	default:
		return "", false, fmt.Errorf("multiple servers are configured; pass --server explicitly")
	}
}
